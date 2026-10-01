// 飞书连接器单测（httptest fake 剑本钉端点形状契约——真租户校准点挂仓 issue）：
// 五面端点路径/信封、wiki 空间白名单、节点树递归、分页、token 失效刷新重试、
// 正文护栏（超限只保 metadata）、权限成员 perm 映射、通讯录部门树。
// fake 与实现共享同一形状契约：端点字符串复刻 = 断言面。
package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tunsuy/agthelm-connectors/contract"
)

// feishuFake 是飞书 API 假面（按请求路径分发；计数器驱动刷新重试断言）。
type feishuFake struct {
	tokenCalls  int
	staleOnce   bool // true = 第一次数据调用回 code 99991663（token 失效面）
	staleServed bool
	contentOver string // 巨型正文文档 id（空 = 无）
}

func (f *feishuFake) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == feishuTokenPath:
			f.tokenCalls++
			body, _ := io.ReadAll(r.Body)
			var req map[string]string
			_ = json.Unmarshal(body, &req)
			if req["app_id"] != "app-1" || req["app_secret"] != "sec-1" {
				t.Errorf("token 凭据失配: %v", req)
			}
			writeBeeo(w, 0, "", map[string]any{
				"tenant_access_token": "t-ok", "expire": 3600,
			})

		case r.URL.Path == feishuWikiSpaces:
			if f.staleOnce && !f.staleServed {
				f.staleServed = true
				writeBeeo(w, 99991663, "token stale", nil) // token 失效面
				return
			}
			if r.URL.Query().Get("page_token") == "" {
				writeBeeo(w, 0, "", map[string]any{
					"items":           []map[string]any{{"space_id": "sp-allow", "name": "知识库A"}, {"space_id": "sp-deny", "name": "知识库B"}},
					"has_more":        true,
					"next_page_token": "pg-2",
				})
				return
			}
			writeBeeo(w, 0, "", map[string]any{"items": []map[string]any{}, "has_more": false})

		case r.URL.Path == feishuFilesPath: // drive 目录枚举（Spaces 项 = folder token）
			writeBeeo(w, 0, "", map[string]any{"files": []map[string]any{}, "has_more": false})

		case r.URL.Path == feishuWikiNodes:
			// 白名单断言在 LoadPipeline 文档计数面（sp-allow 配置 → 只 2 文档）
			q := r.URL.Query()
			// 父层：一个 docx 节点 + 一个带子节点的目录节点
			if q.Get("parent_node_token") == "" {
				writeBeeo(w, 0, "", map[string]any{
					"items": []map[string]any{
						{"node_token": "n-1", "obj_token": "doc-1", "obj_type": "docx", "title": "文档一", "has_child": false, "edited_time": "1760000000"},
						{"node_token": "n-dir", "obj_token": "", "obj_type": "none", "title": "分组", "has_child": true},
					},
					"has_more": false,
				})
				return
			}
			// 子层：doc-2（嵌套节点）+ 巨型文档 doc-big
			bigID := f.contentOver
			items := []map[string]any{
				{"node_token": "n-2", "obj_token": "doc-2", "obj_type": "docx", "title": "文档二", "has_child": false, "edited_time": "1760000100"},
			}
			if bigID != "" {
				items = append(items, map[string]any{
					"node_token": "n-big", "obj_token": bigID, "obj_type": "docx", "title": "巨文档", "has_child": false, "edited_time": "1760000200",
				})
			}
			writeBeeo(w, 0, "", map[string]any{"items": items, "has_more": false})

		case strings.HasPrefix(r.URL.Path, "/open-apis/docx/v1/documents/"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/open-apis/docx/v1/documents/"), "/raw_content")
			if id == f.contentOver {
				writeBeeo(w, 0, "", map[string]any{"content": strings.Repeat("巨", 100)})
				return
			}
			content := "正文-" + id
			writeBeeo(w, 0, "", map[string]any{"content": content})

		case strings.HasPrefix(r.URL.Path, "/open-apis/drive/v1/permissions/"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/open-apis/drive/v1/permissions/"), "/members")
			if r.URL.Query().Get("type") != "docx" {
				t.Errorf("perm type 参数失配: %s", r.URL.RawQuery)
			}
			writeBeeo(w, 0, "", map[string]any{
				"members": []map[string]any{
					{"member_type": "user", "member_id": "ou-1", "perm": "view"},
					{"member_type": "department", "member_id": "dep-1", "perm": "edit"},
				},
				"has_more": false,
			})
			_ = id

		case r.URL.Path == feishuDeptsPath:
			dept := r.URL.Query().Get("department_id")
			if dept == "0" {
				writeBeeo(w, 0, "", map[string]any{
					"items":    []map[string]any{{"department_id": "dep-1", "name": "研发部"}},
					"has_more": false,
				})
				return
			}
			writeBeeo(w, 0, "", map[string]any{"items": []map[string]any{}, "has_more": false})

		case r.URL.Path == feishuUsersPath:
			dept := r.URL.Query().Get("department_id")
			if dept == "0" {
				writeBeeo(w, 0, "", map[string]any{
					"items": []map[string]any{{
						"open_id": "ou-1", "union_id": "un-1", "name": "张三",
						"email": "zhang@example.com", "mobile": "13800000001",
					}},
					"has_more": false,
				})
				return
			}
			writeBeeo(w, 0, "", map[string]any{"items": []map[string]any{}, "has_more": false})

		default:
			t.Errorf("未知端点（形状契约漂移）: %s %s", r.Method, r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
		}
	}
}

// writeBeeo 统一信封 {code, msg, data}。
func writeBeeo(w http.ResponseWriter, code int, msg string, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "msg": msg, "data": data})
}

func newFeishuConn(t *testing.T, fake *feishuFake, spaces []string, maxDoc int64) contract.Connector {
	t.Helper()
	srv := httptest.NewServer(fake.handler(t))
	t.Cleanup(srv.Close)
	conn, err := NewFeishu(FeishuConfig{
		BaseURL: srv.URL, AppID: "app-1", AppSecret: "sec-1", Spaces: spaces, MaxDocBytes: maxDoc,
	})
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	return conn
}

// TestFeishuConfigValidation 配置缺席 → 构造失败（接线判定归调用方告警降级）。
func TestFeishuConfigValidation(t *testing.T) {
	cases := []struct {
		name string
		cfg  FeishuConfig
	}{
		{"缺 BaseURL", FeishuConfig{AppID: "a", AppSecret: "s"}},
		{"缺 AppID", FeishuConfig{BaseURL: "http://x", AppSecret: "s"}},
		{"缺 AppSecret", FeishuConfig{BaseURL: "http://x", AppID: "a"}},
	}
	for _, c := range cases {
		if _, err := NewFeishu(c.cfg); err == nil {
			t.Fatalf("%s 应构造失败", c.name)
		}
	}
}

// TestContractVersion 契约版本钉死（v1；破坏性变更须 +1 + major tag——ADR 口径）。
func TestContractVersion(t *testing.T) {
	if contract.ContractVersion != 1 {
		t.Fatalf("ContractVersion = %d, want 1", contract.ContractVersion)
	}
}

// TestFeishuLoadPipeline 五面全链：文档枚举（白名单 + 递归 + 分页）→ 正文 →
// 权限成员映射 → 通讯录（部门树 + 用户）。
func TestFeishuLoadPipeline(t *testing.T) {
	conn := newFeishuConn(t, &feishuFake{}, []string{"sp-allow"}, 0)
	ctx := context.Background()

	docs, err := conn.LoadDocs(ctx)
	if err != nil {
		t.Fatalf("LoadDocs: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("应 2 文档（doc-1 顶层 + doc-2 嵌套；sp-deny 白名单滤除）: %+v", docs)
	}
	byID := map[string]struct{ raw, title, space string }{}
	for _, d := range docs {
		byID[d.ExternalID] = struct{ raw, title, space string }{string(d.Raw), d.Title, d.SpaceID}
	}
	got := byID["doc-1"]
	if got.raw != "正文-doc-1" || got.title != "文档一" || got.space != "sp-allow" {
		t.Fatalf("doc-1 投影失配: %+v", got)
	}
	if byID["doc-2"].raw != "正文-doc-2" {
		t.Fatalf("doc-2（嵌套层）失配: %+v", byID["doc-2"])
	}
	if docs[0].Platform != "feishu" || docs[0].ContentFormat != feishuContentType || docs[0].ContentHash == "" {
		t.Fatalf("文档平台/format/hash 失配: %+v", docs[0])
	}
	if docs[0].SourceUpdatedAt.IsZero() {
		t.Fatal("SourceUpdatedAt 应自 edited_time 解析")
	}

	// PollDocs：全集 + cursor = 本轮最大水位
	_, cursor, err := conn.PollDocs(ctx, "")
	if err != nil {
		t.Fatalf("PollDocs: %v", err)
	}
	if cursor == "" {
		t.Fatal("cursor 应非空（max modified_time）")
	}

	// 权限成员：view→read / edit→write
	acls, err := conn.LoadACLs(ctx)
	if err != nil {
		t.Fatalf("LoadACLs: %v", err)
	}
	if len(acls) != 4 { // 2 docs × 2 members
		t.Fatalf("应 4 条 ACL: %+v", acls)
	}
	permBy := map[string]string{}
	for _, a := range acls {
		if a.Platform != "feishu" || a.DocRef == "" {
			t.Fatalf("ACL 投影失配: %+v", a)
		}
		permBy[a.MemberType+"/"+a.MemberID] = a.Perm
	}
	if permBy["user/ou-1"] != "read" || permBy["department/dep-1"] != "write" {
		t.Fatalf("perm 映射失配（view→read / edit→write）: %v", permBy)
	}

	// 通讯录：根部门用户 + 部门树
	contacts, depts, err := conn.LoadDirectory(ctx)
	if err != nil {
		t.Fatalf("LoadDirectory: %v", err)
	}
	if len(contacts) != 1 || contacts[0].OpenID != "ou-1" || contacts[0].Email != "zhang@example.com" {
		t.Fatalf("contacts 失配: %+v", contacts)
	}
	if len(depts) != 1 || depts[0].SourceDeptID != "dep-1" || depts[0].Name != "研发部" || depts[0].ParentSourceID != "0" {
		t.Fatalf("depts 失配: %+v", depts)
	}
}

// TestFeishuTokenRefreshRetry token 失效（code 99991663）→ 刷新一次重试成功。
func TestFeishuTokenRefreshRetry(t *testing.T) {
	fake := &feishuFake{staleOnce: true}
	conn := newFeishuConn(t, fake, []string{"sp-allow"}, 0)
	if _, err := conn.LoadDocs(context.Background()); err != nil {
		t.Fatalf("token 失效重试应成功: %v", err)
	}
	if fake.tokenCalls != 2 {
		t.Fatalf("token 应取 2 次（初次 + 失效刷新），得 %d", fake.tokenCalls)
	}
}

// TestFeishuOversizeGuard 正文护栏：超 MaxDocBytes 只保 metadata（Oversize=true、
// Raw 零），且权限面跳过该 doc（无正文可服务）。
func TestFeishuOversizeGuard(t *testing.T) {
	fake := &feishuFake{contentOver: "doc-big"}
	conn := newFeishuConn(t, fake, []string{"sp-allow"}, 50)
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
		} else if d.Oversize || d.Raw == nil {
			t.Fatalf("正常文档被误伤: %+v", d)
		}
	}
	if !found {
		t.Fatalf("oversize 文档应在列: %+v", docs)
	}
	// 权限面跳过 oversize doc（2 正常文档 × 2 成员 = 4）
	acls, err := conn.LoadACLs(ctx)
	if err != nil {
		t.Fatalf("LoadACLs: %v", err)
	}
	if len(acls) != 4 {
		t.Fatalf("oversize doc 不应入权限面: %d 条", len(acls))
	}
}

// TestFeishuNoSpacesMeansAllDrive 空白名单 + 无 drive 目录 → 只余 wiki 全空间
// （sp-deny 也应被枚举——空 = 全部发现的空间）。
func TestFeishuNoSpacesMeansAllDrive(t *testing.T) {
	conn := newFeishuConn(t, &feishuFake{}, nil, 0)
	docs, err := conn.LoadDocs(context.Background())
	if err != nil {
		t.Fatalf("LoadDocs: %v", err)
	}
	if len(docs) != 4 { // sp-allow + sp-deny 各 2 文档
		t.Fatalf("空 Spaces = 全空间，应 4 文档: %d", len(docs))
	}
}

// TestFeishuAPIError 信封业务失败（code != 0）→ error 面向上抛（宿主记 run FAIL）。
func TestFeishuAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == feishuTokenPath { // token 面正常（失败钉在数据面）
			writeBeeo(w, 0, "", map[string]any{"tenant_access_token": "t", "expire": 60})
			return
		}
		writeBeeo(w, 40001, "param invalid", nil)
	}))
	t.Cleanup(srv.Close)
	conn, err := NewFeishu(FeishuConfig{BaseURL: srv.URL, AppID: "a", AppSecret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.LoadDocs(context.Background()); err == nil {
		t.Fatal("业务失败应上抛 error")
	} else if !strings.Contains(fmt.Sprint(err), "40001") {
		t.Fatalf("error 应含业务码: %v", err)
	}
}
