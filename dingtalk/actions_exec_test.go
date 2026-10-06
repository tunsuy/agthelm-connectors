// 钉钉操作面单测（httptest fake 剑本钉端点形状契约——真租户校准点挂仓 issue）：
// 审批创建/查询 + 待办创建端点、processCode 映射与表单组件序、OnBehalf 个人
// 令牌直发（不取应用 token）、应用 token 401 失效刷新重试一次、失败语义归类
// （403 → forbidden）、due 参数两种口径解析、必填校验、状态枚举归一。
package dingtalk

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tunsuy/agthelm-connectors/contract"
)

// dingActionFake 钉钉操作面 API 假面（v1.0 直接 JSON，无统一信封）。
type dingActionFake struct {
	tokenCalls  int
	createCalls int
	staleOnce   bool // true = 第一次审批调用回 http 401（token 失效面）
	staleServed bool
	http403     bool // true = 审批创建回 http 403（forbidden 传输面）
	createBody  map[string]any
	todoBody    map[string]any
	authSeen    []string // 各数据端点收到的 access-token 头（按序）
}

func (f *dingActionFake) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.authSeen = append(f.authSeen, r.Header.Get("x-acs-dingtalk-access-token"))
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

		case r.URL.Path == dingApprovalCreatePath && r.Method == http.MethodPost:
			f.createCalls++
			body, _ := io.ReadAll(r.Body)
			f.createBody = map[string]any{}
			_ = json.Unmarshal(body, &f.createBody)
			if f.http403 {
				http.Error(w, "denied", http.StatusForbidden)
				return
			}
			if f.staleOnce && !f.staleServed {
				f.staleServed = true
				http.Error(w, "token stale", http.StatusUnauthorized)
				return
			}
			writeJSON(w, map[string]any{"instanceId": "inst-d1"})

		case strings.HasPrefix(r.URL.Path, "/v1.0/workflow/processInstances/") && r.Method == http.MethodGet:
			id := strings.TrimPrefix(r.URL.Path, "/v1.0/workflow/processInstances/")
			shape := map[string]map[string]string{
				"inst-d1":     {"status": "RUNNING"},
				"inst-done":   {"status": "COMPLETED", "result": "agree"},
				"inst-refuse": {"status": "COMPLETED", "result": "refuse"},
				"inst-term":   {"status": "TERMINATED"},
				"inst-weird":  {"status": "WEIRD_STATUS"},
			}[id]
			if shape == nil {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			writeJSON(w, map[string]any{"title": "补卡", "status": shape["status"], "result": shape["result"]})

		case r.URL.Path == dingTodoCreatePath && r.Method == http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			f.todoBody = map[string]any{}
			_ = json.Unmarshal(body, &f.todoBody)
			writeJSON(w, map[string]any{"taskId": "todo-9"})

		default:
			t.Errorf("未知端点（形状契约漂移）: %s %s", r.Method, r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
		}
	}
}

func newDingTalkAction(t *testing.T, fake *dingActionFake) contract.ActionConnector {
	t.Helper()
	srv := httptest.NewServer(fake.handler(t))
	t.Cleanup(srv.Close)
	conn, err := NewDingTalkAction(DingTalkActionConfig{
		BaseURL: srv.URL, AppKey: "key-1", AppSecret: "sec-1",
		ProcessCodes: map[string]string{"补卡": "pc-ding"},
	})
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	return conn
}

func execErrKind(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatalf("预期失败，得到 nil")
	}
	ee, ok := err.(*contract.ExecError)
	if !ok {
		t.Fatalf("预期 *contract.ExecError，得到 %T: %v", err, err)
	}
	return ee.Kind
}

// TestDingTalkActionConfigValidation 配置缺席 → 构造失败。
func TestDingTalkActionConfigValidation(t *testing.T) {
	if _, err := NewDingTalkAction(DingTalkActionConfig{AppKey: "k", AppSecret: "s"}); err == nil {
		t.Error("缺 BaseURL 应失败")
	}
	if _, err := NewDingTalkAction(DingTalkActionConfig{BaseURL: "http://x", AppKey: "k"}); err == nil {
		t.Error("缺 AppSecret 应失败")
	}
}

// TestDingTalkActionSubmit 应用身份发起审批：类型键走映射取 processCode；必填
// person 路由进 originatorUserId；可选参数进表单组件（键序确定性）。
func TestDingTalkActionSubmit(t *testing.T) {
	fake := &dingActionFake{}
	conn := newDingTalkAction(t, fake)
	rc, err := conn.Exec(context.Background(), contract.ExecRequest{
		Action: "submit_approval",
		Params: map[string]string{"type": "补卡", "person": "user-1", "date": "2026-10-05", "reason": "忘打卡"},
	})
	if err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	if rc.Ref != "inst-d1" || !strings.Contains(rc.Message, "inst-d1") {
		t.Errorf("回执失配: %+v", rc)
	}
	if fake.tokenCalls != 1 {
		t.Errorf("应用 token 应取一次: %d", fake.tokenCalls)
	}
	if len(fake.authSeen) != 2 || fake.authSeen[1] != "t-ok" {
		t.Errorf("数据端点应带应用 token 头: %v", fake.authSeen)
	}
	if fake.createBody["processCode"] != "pc-ding" {
		t.Errorf("processCode 应走映射: %v", fake.createBody["processCode"])
	}
	if fake.createBody["originatorUserId"] != "user-1" {
		t.Errorf("originatorUserId 失配: %v", fake.createBody["originatorUserId"])
	}
	form, _ := fake.createBody["formComponentValues"].([]any)
	if len(form) != 2 {
		t.Fatalf("表单组件应 2 行（排除路由键）: %v", form)
	}
	if form[0].(map[string]any)["name"] != "date" || form[1].(map[string]any)["name"] != "reason" {
		t.Errorf("表单组件应按键序: %v", form)
	}
}

// TestDingTalkActionOnBehalf 发起人个人令牌直发：不取应用 token（ADR-012：
// 令牌归宿主，操作面不缓存不刷新）。
func TestDingTalkActionOnBehalf(t *testing.T) {
	fake := &dingActionFake{}
	conn := newDingTalkAction(t, fake)
	_, err := conn.Exec(context.Background(), contract.ExecRequest{
		Action:   "submit_approval",
		Params:   map[string]string{"type": "补卡", "person": "user-1"},
		OnBehalf: "u-token-9",
	})
	if err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	if fake.tokenCalls != 0 {
		t.Errorf("OnBehalf 不应取应用 token: %d", fake.tokenCalls)
	}
	if len(fake.authSeen) != 1 || fake.authSeen[0] != "u-token-9" {
		t.Errorf("数据端点应带发起人令牌: %v", fake.authSeen)
	}
}

// TestDingTalkActionTokenStaleRetry 应用 token 401 失效 → 刷新一次重试成功。
func TestDingTalkActionTokenStaleRetry(t *testing.T) {
	fake := &dingActionFake{staleOnce: true}
	conn := newDingTalkAction(t, fake)
	rc, err := conn.Exec(context.Background(), contract.ExecRequest{
		Action: "submit_approval",
		Params: map[string]string{"type": "补卡", "person": "user-1"},
	})
	if err != nil {
		t.Fatalf("重试后应成功: %v", err)
	}
	if rc.Ref != "inst-d1" {
		t.Errorf("回执失配: %+v", rc)
	}
	if fake.tokenCalls != 2 {
		t.Errorf("应刷新一次重取 token: %d", fake.tokenCalls)
	}
}

// TestDingTalkActionForbidden http 403 → forbidden 透传（不吞不改写）。
func TestDingTalkActionForbidden(t *testing.T) {
	fake := &dingActionFake{http403: true}
	conn := newDingTalkAction(t, fake)
	_, err := conn.Exec(context.Background(), contract.ExecRequest{
		Action: "submit_approval",
		Params: map[string]string{"type": "补卡", "person": "user-1"},
	})
	if kind := execErrKind(t, err); kind != contract.ExecKindForbidden {
		t.Errorf("应归类 forbidden: %s", kind)
	}
	if !strings.Contains(err.Error(), "http 403") {
		t.Errorf("应透传平台原文: %v", err)
	}
}

// TestDingTalkActionQueryStatus 查询状态枚举归一（未知枚举原文透出不猜）。
func TestDingTalkActionQueryStatus(t *testing.T) {
	fake := &dingActionFake{}
	conn := newDingTalkAction(t, fake)
	cases := map[string]string{
		"inst-d1":     "审批状态：审批中",
		"inst-done":   "审批状态：已完成（已同意）",
		"inst-refuse": "审批状态：已完成（已拒绝）",
		"inst-term":   "审批状态：已终止",
		"inst-weird":  "审批状态：WEIRD_STATUS",
	}
	for ref, want := range cases {
		rc, err := conn.Exec(context.Background(), contract.ExecRequest{
			Action: "query_approval",
			Params: map[string]string{"ref": ref},
		})
		if err != nil {
			t.Fatalf("查询 %s 失败: %v", ref, err)
		}
		if rc.Message != want {
			t.Errorf("%s: %q != %q", ref, rc.Message, want)
		}
	}
}

// TestDingTalkActionCreateTodo 待办创建：unionId/subject 路由 + due 两口径
// （RFC3339 与 unix 秒 → 毫秒）；坏 due → invalid_params 不猜。
func TestDingTalkActionCreateTodo(t *testing.T) {
	due := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	fake := &dingActionFake{}
	conn := newDingTalkAction(t, fake)
	rc, err := conn.Exec(context.Background(), contract.ExecRequest{
		Action: "create_todo",
		Params: map[string]string{"title": "回访客户", "person": "un-1", "due": due.Format(time.RFC3339)},
	})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if rc.Ref != "todo-9" || !strings.Contains(rc.Message, "回访客户") {
		t.Errorf("回执失配: %+v", rc)
	}
	if fake.todoBody["unionId"] != "un-1" || fake.todoBody["subject"] != "回访客户" {
		t.Errorf("todo 体失配: %v", fake.todoBody)
	}
	if ms, ok := fake.todoBody["dueTime"].(float64); !ok || int64(ms) != due.UnixMilli() {
		t.Errorf("dueTime 应为 RFC3339 毫秒口径: %v", fake.todoBody["dueTime"])
	}

	rc, err = conn.Exec(context.Background(), contract.ExecRequest{
		Action: "create_todo",
		Params: map[string]string{"title": "催办", "person": "un-1", "due": strconv.FormatInt(due.Unix(), 10)},
	})
	if err != nil {
		t.Fatalf("unix 秒口径应可解析: %v", err)
	}
	if ms, ok := fake.todoBody["dueTime"].(float64); !ok || int64(ms) != due.UnixMilli() {
		t.Errorf("dueTime 应为 unix 秒→毫秒口径: %v", fake.todoBody["dueTime"])
	}

	_, err = conn.Exec(context.Background(), contract.ExecRequest{
		Action: "create_todo",
		Params: map[string]string{"title": "坏参数", "person": "un-1", "due": "明天"},
	})
	if kind := execErrKind(t, err); kind != contract.ExecKindInvalidParams {
		t.Errorf("坏 due 应 invalid_params: %s", kind)
	}
}

// TestDingTalkActionInvalidParams 未知操作 / 缺必填 → invalid_params（不触达平台）。
func TestDingTalkActionInvalidParams(t *testing.T) {
	fake := &dingActionFake{}
	conn := newDingTalkAction(t, fake)

	_, err := conn.Exec(context.Background(), contract.ExecRequest{Action: "drop_table"})
	if kind := execErrKind(t, err); kind != contract.ExecKindInvalidParams {
		t.Errorf("未知操作应 invalid_params: %s", kind)
	}

	_, err = conn.Exec(context.Background(), contract.ExecRequest{
		Action: "create_todo",
		Params: map[string]string{"person": "un-1"}, // 缺 title
	})
	if err == nil || !strings.Contains(err.Error(), "待办标题") {
		t.Errorf("缺 title 应报「缺少必填参数」含标签: %v", err)
	}
	if fake.createCalls != 0 {
		t.Errorf("参数缺失不应触达平台: %d", fake.createCalls)
	}
}
