// 飞书操作面单测（httptest fake 剑本钉端点形状契约——真租户校准点挂仓 issue）：
// 审批创建/查询端点 + 参数路由（approval_code 映射 / form 组件构造）、OnBehalf
// 用户令牌直发（不取应用 token）、应用 token 失效刷新重试一次、失败语义归类
// （forbidden 透传 / unauthorized 诚实回执不降级）、必填校验、状态枚举归一。
package feishu

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

// feishuActionFake 飞书操作面 API 假面（按请求路径分发；计数器与记录器驱动断言）。
type feishuActionFake struct {
	tokenCalls   int
	createCalls  int
	staleOnce    bool // true = 第一次审批调用回 code 99991663（token 失效面）
	staleServed  bool
	deniedOnce   bool // true = 审批调用回 code 99991672（权限不足面）
	deniedServed bool
	http403      bool // true = 审批创建回 http 403（forbidden 传输面）
	createBody   map[string]any
	authSeen     []string // 各数据端点收到的 Authorization（按序）
}

func (f *feishuActionFake) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.authSeen = append(f.authSeen, r.Header.Get("Authorization"))
		switch {
		case r.URL.Path == feishuTokenPath:
			f.tokenCalls++
			body, _ := io.ReadAll(r.Body)
			var req map[string]string
			_ = json.Unmarshal(body, &req)
			if req["app_id"] != "app-1" || req["app_secret"] != "sec-1" {
				t.Errorf("token 凭据失配: %v", req)
			}
			writeBeeo(w, 0, "", map[string]any{"tenant_access_token": "t-ok", "expire": 3600})

		case r.URL.Path == feishuApprovalCreatePath && r.Method == http.MethodPost:
			if r.URL.Query().Get("user_id_type") != "open_id" {
				t.Errorf("user_id_type 参数失配: %s", r.URL.RawQuery)
			}
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
				writeBeeo(w, 99991663, "token stale", nil)
				return
			}
			if f.deniedOnce && !f.deniedServed {
				f.deniedServed = true
				writeBeeo(w, 99991672, "no permission", nil)
				return
			}
			writeBeeo(w, 0, "", map[string]any{"instance_code": "inst-42"})

		case strings.HasPrefix(r.URL.Path, "/open-apis/approval/v4/instances/") && r.Method == http.MethodGet:
			id := strings.TrimPrefix(r.URL.Path, "/open-apis/approval/v4/instances/")
			status := map[string]string{
				"inst-42": "PENDING", "inst-ok": "APPROVED", "inst-no": "REJECTED", "inst-weird": "WEIRD_STATUS",
			}[id]
			writeBeeo(w, 0, "", map[string]any{"instance": map[string]any{"approval_code": "pc-code", "status": status}})

		default:
			t.Errorf("未知端点（形状契约漂移）: %s %s", r.Method, r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
		}
	}
}

func newFeishuAction(t *testing.T, fake *feishuActionFake) contract.ActionConnector {
	t.Helper()
	srv := httptest.NewServer(fake.handler(t))
	t.Cleanup(srv.Close)
	conn, err := NewFeishuAction(FeishuActionConfig{
		BaseURL: srv.URL, AppID: "app-1", AppSecret: "sec-1",
		ApprovalCodes: map[string]string{"补卡": "pc-code"},
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

// TestFeishuActionConfigValidation 配置缺席 → 构造失败。
func TestFeishuActionConfigValidation(t *testing.T) {
	if _, err := NewFeishuAction(FeishuActionConfig{AppID: "a", AppSecret: "s"}); err == nil {
		t.Error("缺 BaseURL 应失败")
	}
	if _, err := NewFeishuAction(FeishuActionConfig{BaseURL: "http://x", AppSecret: "s"}); err == nil {
		t.Error("缺 AppID 应失败")
	}
}

// TestFeishuActionSubmit 应用身份发起审批：类型键走映射取 approval_code；必填
// person 路由进 user_id；可选参数进 form 组件；回执带单号。
func TestFeishuActionSubmit(t *testing.T) {
	fake := &feishuActionFake{}
	conn := newFeishuAction(t, fake)
	rc, err := conn.Exec(context.Background(), contract.ExecRequest{
		Action: "submit_approval",
		Params: map[string]string{"type": "补卡", "person": "ou-1", "date": "2026-10-05", "reason": "忘打卡"},
	})
	if err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	if rc.Ref != "inst-42" {
		t.Errorf("Ref 失配: %s", rc.Ref)
	}
	if !strings.Contains(rc.Message, "inst-42") {
		t.Errorf("Message 应带单号: %s", rc.Message)
	}
	if fake.tokenCalls != 1 {
		t.Errorf("应用 token 应取一次: %d", fake.tokenCalls)
	}
	if len(fake.authSeen) != 2 || fake.authSeen[1] != "Bearer t-ok" {
		t.Errorf("数据端点应带应用 Bearer: %v", fake.authSeen)
	}
	if fake.createBody["approval_code"] != "pc-code" {
		t.Errorf("approval_code 应走映射: %v", fake.createBody["approval_code"])
	}
	if fake.createBody["user_id"] != "ou-1" {
		t.Errorf("user_id 失配: %v", fake.createBody["user_id"])
	}
	form, _ := fake.createBody["form"].(string)
	if !strings.Contains(form, `"date":"2026-10-05"`) || !strings.Contains(form, `"reason":"忘打卡"`) {
		t.Errorf("form 组件应含可选参数: %s", form)
	}
	if strings.Contains(form, "type") || strings.Contains(form, "person") {
		t.Errorf("form 不应含路由键: %s", form)
	}
}

// TestFeishuActionSubmitUnmappedType 无映射类型键 = 原文直用（真租户校准点）。
func TestFeishuActionSubmitUnmappedType(t *testing.T) {
	fake := &feishuActionFake{}
	conn := newFeishuAction(t, fake)
	_, err := conn.Exec(context.Background(), contract.ExecRequest{
		Action: "submit_approval",
		Params: map[string]string{"type": "报销", "person": "ou-1"},
	})
	if err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	if fake.createBody["approval_code"] != "报销" {
		t.Errorf("无映射应原文直用: %v", fake.createBody["approval_code"])
	}
}

// TestFeishuActionOnBehalf 发起人用户令牌直发：不取应用 token（ADR-012：令牌
// 归宿主，操作面不缓存不刷新）。
func TestFeishuActionOnBehalf(t *testing.T) {
	fake := &feishuActionFake{}
	conn := newFeishuAction(t, fake)
	_, err := conn.Exec(context.Background(), contract.ExecRequest{
		Action:   "submit_approval",
		Params:   map[string]string{"type": "补卡", "person": "ou-1"},
		OnBehalf: "u-token-9",
	})
	if err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	if fake.tokenCalls != 0 {
		t.Errorf("OnBehalf 不应取应用 token: %d", fake.tokenCalls)
	}
	if len(fake.authSeen) != 1 || fake.authSeen[0] != "Bearer u-token-9" {
		t.Errorf("数据端点应带发起人令牌: %v", fake.authSeen)
	}
}

// TestFeishuActionOnBehalfStale 发起人令牌失效 = unauthorized 诚实回执：不刷新、
// 不降级应用身份重试（ADR-012 红线——静默降级禁止）。
func TestFeishuActionOnBehalfStale(t *testing.T) {
	fake := &feishuActionFake{staleOnce: true}
	conn := newFeishuAction(t, fake)
	_, err := conn.Exec(context.Background(), contract.ExecRequest{
		Action:   "submit_approval",
		Params:   map[string]string{"type": "补卡", "person": "ou-1"},
		OnBehalf: "u-token-stale",
	})
	if kind := execErrKind(t, err); kind != contract.ExecKindUnauthorized {
		t.Errorf("失效应归类 unauthorized: %s", kind)
	}
	if fake.tokenCalls != 0 || fake.createCalls != 1 {
		t.Errorf("不应刷新/重试: token=%d create=%d", fake.tokenCalls, fake.createCalls)
	}
}

// TestFeishuActionTokenStaleRetry 应用 token 失效 → 刷新一次重试成功（失效注入
// 面在创建端点——查询端点同信封口径，一处验证即钉契约）。
func TestFeishuActionTokenStaleRetry(t *testing.T) {
	fake := &feishuActionFake{staleOnce: true}
	conn := newFeishuAction(t, fake)
	rc, err := conn.Exec(context.Background(), contract.ExecRequest{
		Action: "submit_approval",
		Params: map[string]string{"type": "补卡", "person": "ou-1"},
	})
	if err != nil {
		t.Fatalf("重试后应成功: %v", err)
	}
	if rc.Ref != "inst-42" {
		t.Errorf("回执失配: %+v", rc)
	}
	if fake.tokenCalls != 2 {
		t.Errorf("应刷新一次重取 token: %d", fake.tokenCalls)
	}
}

// TestFeishuActionForbidden 权限不足（信封 99991672 / http 403）→ forbidden
// 透传（不吞不改写——平台原文进 Message）。
func TestFeishuActionForbidden(t *testing.T) {
	for name, fake := range map[string]*feishuActionFake{
		"信封 denied": {deniedOnce: true},
		"http 403":  {http403: true},
	} {
		t.Run(name, func(t *testing.T) {
			conn := newFeishuAction(t, fake)
			_, err := conn.Exec(context.Background(), contract.ExecRequest{
				Action: "submit_approval",
				Params: map[string]string{"type": "补卡", "person": "ou-1"},
			})
			if kind := execErrKind(t, err); kind != contract.ExecKindForbidden {
				t.Errorf("应归类 forbidden: %s", kind)
			}
			if !strings.Contains(err.Error(), "99991672") && name == "信封 denied" {
				t.Errorf("应透传平台码: %v", err)
			}
		})
	}
}

// TestFeishuActionQueryStatus 查询状态枚举归一（未知枚举原文透出不猜）。
func TestFeishuActionQueryStatus(t *testing.T) {
	fake := &feishuActionFake{}
	conn := newFeishuAction(t, fake)
	cases := map[string]string{
		"inst-42":    "审批状态：审批中",
		"inst-ok":    "审批状态：已通过",
		"inst-no":    "审批状态：已拒绝",
		"inst-weird": "审批状态：WEIRD_STATUS",
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

// TestFeishuActionInvalidParams 未知操作 / 缺必填 → invalid_params（逐项报齐）。
func TestFeishuActionInvalidParams(t *testing.T) {
	fake := &feishuActionFake{}
	conn := newFeishuAction(t, fake)

	_, err := conn.Exec(context.Background(), contract.ExecRequest{Action: "drop_table"})
	if kind := execErrKind(t, err); kind != contract.ExecKindInvalidParams {
		t.Errorf("未知操作应 invalid_params: %s", kind)
	}
	if fake.createCalls != 0 {
		t.Errorf("未知操作不应触达平台: %d", fake.createCalls)
	}

	_, err = conn.Exec(context.Background(), contract.ExecRequest{
		Action: "submit_approval",
		Params: map[string]string{"person": "ou-1"}, // 缺 type
	})
	if err == nil || !strings.Contains(err.Error(), "审批类型") {
		t.Errorf("缺 type 应报「缺少必填参数」含标签: %v", err)
	}
}
