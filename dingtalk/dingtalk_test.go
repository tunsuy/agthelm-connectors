// 钉钉连接器单测（httptest fake 剑本钉端点形状契约——真租户校准点挂仓 issue）：
// v1.0 直 JSON + topapi errcode 双信封、空间/dentries 分页、401 token 失效刷新重试、
// topapi errcode=88 失效面、正文护栏、权限成员 role 映射、部门树 + 用户 cursor 分页去重。
// fake 与实现共享同一形状契约：端点字符串复刻 = 断言面。
package dingtalk

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tunsuy/agthelm-connectors/contract"
)

// dingtalkFake 是钉钉 API 假面（v1.0 + topapi 双风格；计数器驱动刷新重试断言）。
type dingtalkFake struct {
	tokenCalls  int
	unauthOnce  bool // true = 第一次数据调用回 401（v1.0 token 失效面）
	unauthHit   bool
	err88Once   bool // true = topapi 首次回 errcode=88（旧风格失效面）
	err88Hit    bool
	contentOver string
}

func (f *dingtalkFake) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if tok := r.Header.Get("x-acs-dingtalk-access-token"); tok != "" && tok != "t-ok" {
			t.Errorf("token 头失配: %q", tok)
		}
		switch {
		case r.URL.Path == dingtalkTokenPath:
			f.tokenCalls++
			body, _ := io.ReadAll(r.Body)
			var req map[string]string
			_ = json.Unmarshal(body, &req)
			if req["appKey"] != "key-1" || req["appSecret"] != "sec-1" {
				t.Errorf("token 凭据失配: %v", req)
			}
			writeJSON(w, map[string]any{"accessToken": "t-ok", "expireIn": 3600})

		case r.URL.Path == dingtalkSpacesPath:
			if f.unauthOnce && !f.unauthHit {
				f.unauthHit = true
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if r.URL.Query().Get("nextToken") == "" {
				writeJSON(w, map[string]any{
					"spaces":    []map[string]any{{"id": "sp-1", "name": "空间A"}, {"id": "sp-2", "name": "空间B"}},
					"nextToken": "nt-2",
				})
				return
			}
			writeJSON(w, map[string]any{"spaces": []map[string]any{}, "nextToken": ""})

		case strings.HasPrefix(r.URL.Path, "/v1.0/storage/spaces/") && strings.HasSuffix(r.URL.Path, "/dentries"):
			space := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1.0/storage/spaces/"), "/dentries")
			if f.unauthOnce && !f.unauthHit {
				f.unauthHit = true
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_ = space
			bigID := f.contentOver
			entries := []map[string]any{
				{"id": "doc-1", "name": "文档一", "type": "doc", "updatedAt": "1760000000000", "creatorId": "cr-1"},
				{"id": "folder-1", "name": "目录", "type": "folder", "updatedAt": "1760000000000", "creatorId": "cr-1"},
			}
			if bigID != "" {
				entries = append(entries, map[string]any{
					"id": bigID, "name": "巨文档", "type": "doc", "updatedAt": "1760000200000", "creatorId": "cr-1",
				})
			}
			writeJSON(w, map[string]any{"dentries": entries, "nextToken": ""})

		case strings.HasPrefix(r.URL.Path, "/v1.0/doc/documents/"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1.0/doc/documents/"), "/rawContent")
			if id == f.contentOver {
				writeJSON(w, map[string]any{"content": strings.Repeat("巨", 100)})
				return
			}
			writeJSON(w, map[string]any{"content": "正文-" + id})

		case strings.HasPrefix(r.URL.Path, "/v1.0/drive/spaces/"):
			writeJSON(w, map[string]any{
				"members": []map[string]any{
					{"memberType": "user", "memberId": "usr-1", "role": "reader"},
					{"memberType": "department", "memberId": "dep-1", "role": "editor"},
				},
			})

		case r.URL.Path == dingtalkDeptPath:
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			if f.err88Once && !f.err88Hit {
				f.err88Hit = true
				writeJSON(w, map[string]any{"errcode": 88, "errmsg": "token stale"})
				return
			}
			if req["deptId"].(float64) == 1 {
				writeJSON(w, map[string]any{"errcode": 0, "result": []map[string]any{
					{"dept_id": 101, "name": "研发部"},
				}})
				return
			}
			writeJSON(w, map[string]any{"errcode": 0, "result": []map[string]any{}})

		case r.URL.Path == dingtalkUserPath:
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req["deptId"].(float64) == 1 {
				// cursor 0：张三 + has_more；cursor 50：李四（多部门归属重复出现，seen 去重面）
				if req["cursor"].(float64) == 0 {
					writeJSON(w, map[string]any{"errcode": 0, "result": map[string]any{
						"has_more": true,
						"list": []map[string]any{{
							"userid": "usr-1", "unionid": "un-1", "name": "张三",
							"email": "zhang@example.com", "mobile": "13800000001",
						}},
					}})
					return
				}
				writeJSON(w, map[string]any{"errcode": 0, "result": map[string]any{
					"has_more": false,
					"list": []map[string]any{{
						"userid": "usr-2", "unionid": "un-2", "name": "李四",
						"email": "li@example.com", "mobile": "13800000002",
					}},
				}})
				return
			}
			// 子部门 101：张三重复出现（多部门归属）——seen 去重
			writeJSON(w, map[string]any{"errcode": 0, "result": map[string]any{
				"has_more": false,
				"list": []map[string]any{{
					"userid": "usr-1", "unionid": "un-1", "name": "张三",
					"email": "zhang@example.com", "mobile": "13800000001",
				}},
			}})

		default:
			t.Errorf("未知端点（形状契约漂移）: %s %s", r.Method, r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func newDingTalkConn(t *testing.T, fake *dingtalkFake, spaces []string, maxDoc int64) contract.Connector {
	t.Helper()
	srv := httptest.NewServer(fake.handler(t))
	t.Cleanup(srv.Close)
	conn, err := NewDingTalk(DingTalkConfig{
		BaseURL: srv.URL, AppKey: "key-1", AppSecret: "sec-1", Spaces: spaces, MaxDocBytes: maxDoc,
	})
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	return conn
}

// TestDingTalkConfigValidation 配置缺席 → 构造失败。
func TestDingTalkConfigValidation(t *testing.T) {
	cases := []struct {
		name string
		cfg  DingTalkConfig
	}{
		{"缺 BaseURL", DingTalkConfig{AppKey: "k", AppSecret: "s"}},
		{"缺 AppKey", DingTalkConfig{BaseURL: "http://x", AppSecret: "s"}},
		{"缺 AppSecret", DingTalkConfig{BaseURL: "http://x", AppKey: "k"}},
	}
	for _, c := range cases {
		if _, err := NewDingTalk(c.cfg); err == nil {
			t.Fatalf("%s 应构造失败", c.name)
		}
	}
}

// TestDingTalkLoadPipeline 五面全链：空间（分页 + 白名单）→ dentries（folder 滤除）→
// 正文（毫秒时间戳）→ 权限 role 映射 → 部门树 + 用户（cursor 分页 + seen 去重）。
func TestDingTalkLoadPipeline(t *testing.T) {
	conn := newDingTalkConn(t, &dingtalkFake{}, []string{"sp-1"}, 0)
	ctx := context.Background()

	docs, err := conn.LoadDocs(ctx)
	if err != nil {
		t.Fatalf("LoadDocs: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("应 1 文档（sp-2 白名单滤除、folder 类型滤除）: %+v", docs)
	}
	d := docs[0]
	if d.Platform != "dingtalk" || d.ExternalID != "doc-1" || d.Title != "文档一" || d.OwnerID != "cr-1" {
		t.Fatalf("文档投影失配: %+v", d)
	}
	if string(d.Raw) != "正文-doc-1" || d.ContentHash == "" || d.ContentFormat != dingtalkContentType {
		t.Fatalf("正文/hash/format 失配: %+v", d)
	}
	if d.SourceUpdatedAt.UTC().Unix() != 1760000000 { // 毫秒 → 秒
		t.Fatalf("updatedAt 毫秒→秒失配: %v", d.SourceUpdatedAt)
	}

	_, cursor, err := conn.PollDocs(ctx, "")
	if err != nil || cursor == "" {
		t.Fatalf("PollDocs cursor 失配: %q err=%v", cursor, err)
	}

	acls, err := conn.LoadACLs(ctx)
	if err != nil {
		t.Fatalf("LoadACLs: %v", err)
	}
	if len(acls) != 2 {
		t.Fatalf("应 2 条 ACL: %+v", acls)
	}
	permBy := map[string]string{}
	for _, a := range acls {
		permBy[a.MemberType+"/"+a.MemberID] = a.Perm
	}
	if permBy["user/usr-1"] != "read" || permBy["department/dep-1"] != "write" {
		t.Fatalf("role 映射失配（reader→read / editor→write）: %v", permBy)
	}

	contacts, depts, err := conn.LoadDirectory(ctx)
	if err != nil {
		t.Fatalf("LoadDirectory: %v", err)
	}
	if len(depts) != 1 || depts[0].SourceDeptID != "101" || depts[0].ParentSourceID != "1" {
		t.Fatalf("depts 失配: %+v", depts)
	}
	if len(contacts) != 2 { // 张三（多部门出现）去重 + 李四
		t.Fatalf("contacts 应 2（seen 去重）: %+v", contacts)
	}
	if contacts[0].OpenID != "usr-1" || contacts[1].OpenID != "usr-2" {
		t.Fatalf("contacts 顺序/去重失配: %+v", contacts)
	}
}

// TestDingTalk401RefreshRetry v1.0 风格 401 → 刷新一次重试成功。
func TestDingTalk401RefreshRetry(t *testing.T) {
	fake := &dingtalkFake{unauthOnce: true}
	conn := newDingTalkConn(t, fake, nil, 0)
	if _, err := conn.LoadDocs(context.Background()); err != nil {
		t.Fatalf("401 失效重试应成功: %v", err)
	}
	if fake.tokenCalls != 2 {
		t.Fatalf("token 应取 2 次（初次 + 失效刷新），得 %d", fake.tokenCalls)
	}
}

// TestDingTalkErr88RefreshRetry topapi 风格 errcode=88 → 刷新一次重试成功。
func TestDingTalkErr88RefreshRetry(t *testing.T) {
	fake := &dingtalkFake{err88Once: true}
	conn := newDingTalkConn(t, fake, nil, 0)
	if _, _, err := conn.LoadDirectory(context.Background()); err != nil {
		t.Fatalf("errcode=88 失效重试应成功: %v", err)
	}
	if fake.tokenCalls != 2 {
		t.Fatalf("token 应取 2 次，得 %d", fake.tokenCalls)
	}
}

// TestDingTalkOversizeGuard 正文护栏：超 MaxDocBytes 只保 metadata + 权限面跳过。
func TestDingTalkOversizeGuard(t *testing.T) {
	fake := &dingtalkFake{contentOver: "doc-big"}
	conn := newDingTalkConn(t, fake, []string{"sp-1"}, 50)
	ctx := context.Background()
	docs, err := conn.LoadDocs(ctx)
	if err != nil {
		t.Fatalf("LoadDocs: %v", err)
	}
	found := false
	for _, d := range docs {
		if d.ExternalID == "doc-big" {
			found = true
			if !d.Oversize || d.Raw != nil || d.SizeBytes != 300 {
				t.Fatalf("oversize 投影失配: %+v", d)
			}
		}
	}
	if !found {
		t.Fatalf("oversize 文档应在列: %+v", docs)
	}
	acls, err := conn.LoadACLs(ctx)
	if err != nil {
		t.Fatalf("LoadACLs: %v", err)
	}
	if len(acls) != 2 { // 只剩 doc-1 的 2 成员
		t.Fatalf("oversize doc 不应入权限面: %d 条", len(acls))
	}
}

// TestDingTalkAPIError errcode != 0 → error 上抛（宿主记 run FAIL）。
func TestDingTalkAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == dingtalkTokenPath {
			writeJSON(w, map[string]any{"accessToken": "t", "expireIn": 60})
			return
		}
		writeJSON(w, map[string]any{"errcode": 60020, "errmsg": "no permission"})
	}))
	t.Cleanup(srv.Close)
	conn, err := NewDingTalk(DingTalkConfig{BaseURL: srv.URL, AppKey: "k", AppSecret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.LoadDocs(context.Background()); err == nil {
		t.Fatal("业务失败应上抛 error")
	} else if !strings.Contains(err.Error(), "60020") {
		t.Fatalf("error 应含业务码: %v", err)
	}
}
