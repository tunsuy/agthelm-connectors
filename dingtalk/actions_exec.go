// 钉钉操作面（v0.3 增补，additive）：发起审批 / 查审批进度 / 创建待办。复用
// 同步面客户端骨架（accessToken 缓存 + 失效刷新重试）；OnBehalf 非空 = 宿主
// 已取的发起人个人令牌（同一 x-acs-dingtalk-access-token 头直发，不缓存不刷新
// ——令牌归宿主；失效 = unauthorized 诚实回执，由宿主指引补授权），空 = 应用
// accessToken。端点形状按公开文档钉死（真租户校准点挂仓 issue；v1.0 错误体的
// code/message 透传面同上挂 issue——当前非 200 归 platform）。IdempotencyKey
// 平台端点无幂等参数——宿主侧同键去重，adapter 不消费。
package dingtalk

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tunsuy/agthelm-connectors/contract"
)

const (
	dingApprovalCreatePath = "/v1.0/workflow/processInstances"
	dingApprovalGetFmt     = "/v1.0/workflow/processInstances/%s"
	dingTodoCreatePath     = "/v1.0/todo/tasks"
)

// DingTalkActionConfig 操作面配置（BaseURL 可注入 = fake 剑本消费同一实现）。
type DingTalkActionConfig struct {
	BaseURL   string
	AppKey    string // 应用凭据（与同步面同键族——宿主凭据面归一管理）
	AppSecret string
	// ProcessCodes 审批类型键 → 租户流程模板 processCode 映射（钉钉后台建审批
	// 模板后的码；缺映射 = 类型键原文直用——真租户校准点挂仓 issue）。
	ProcessCodes map[string]string
}

// NewDingTalkAction 构造操作面连接器（cfg 校验失败 → error，接线判定归调用方）。
func NewDingTalkAction(cfg DingTalkActionConfig) (contract.ActionConnector, error) {
	if cfg.BaseURL == "" || cfg.AppKey == "" || cfg.AppSecret == "" {
		return nil, fmt.Errorf("dingtalk action: 配置不完整（BaseURL/AppKey/AppSecret）")
	}
	return &dingtalkAction{
		core: dingtalkClient{
			cfg: DingTalkConfig{BaseURL: cfg.BaseURL, AppKey: cfg.AppKey, AppSecret: cfg.AppSecret},
			hc:  &http.Client{Timeout: 30 * time.Second},
		},
		codes: cfg.ProcessCodes,
	}, nil
}

// dingtalkAction 操作面连接器（复用 dingtalkClient 的 do/ensureToken 骨架）。
type dingtalkAction struct {
	core  dingtalkClient
	codes map[string]string
}

// 编译哨兵：操作面实现必须满足 v0.2 契约（漂移 = 编译失败）。
var _ contract.ActionConnector = (*dingtalkAction)(nil)

func (a *dingtalkAction) System() string { return dingtalkPlatform }
func (a *dingtalkAction) Capabilities() []contract.ActionCapability {
	return ActionCapabilities
}

func (a *dingtalkAction) Exec(ctx context.Context, req contract.ExecRequest) (contract.ExecReceipt, error) {
	cap, ok := actionCapability(req.Action)
	if !ok {
		return contract.ExecReceipt{}, contract.ExecErr(contract.ExecKindInvalidParams, "", "未知操作："+req.Action, false)
	}
	if err := contract.ValidateParams(cap, req.Params); err != nil {
		return contract.ExecReceipt{}, err
	}
	switch req.Action {
	case "submit_approval":
		return a.execSubmit(ctx, req)
	case "query_approval":
		return a.execQuery(ctx, req)
	case "create_todo":
		return a.execCreateTodo(ctx, req)
	default:
		return contract.ExecReceipt{}, contract.ExecErr(contract.ExecKindInvalidParams, "", "未知操作："+req.Action, false)
	}
}

// actionCapability 能力声明查行（Exec 分发与参数校验同源——包内共用）。
func actionCapability(key string) (contract.ActionCapability, bool) {
	for _, c := range ActionCapabilities {
		if c.Action == key {
			return c, true
		}
	}
	return contract.ActionCapability{}, false
}

// dingFormValue 表单组件值行（name/value 平铺——形状真租户校准点挂仓 issue）。
type dingFormValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// execSubmit 发起审批：processCode（映射或类型键原文）+ 发起人 + 表单组件值。
func (a *dingtalkAction) execSubmit(ctx context.Context, req contract.ExecRequest) (contract.ExecReceipt, error) {
	typ := req.Params["type"]
	code := a.codes[typ]
	if code == "" {
		code = typ
	}
	body := map[string]any{
		"processCode":         code,
		"originatorUserId":    req.Params["person"], // 发起人（钉钉 userid 口径）
		"formComponentValues": formValues(req.Params),
	}
	var out struct {
		InstanceID string `json:"instanceId"` // 实例号 = 追溯 Ref（宿主幂等键锚点）
	}
	if err := a.callAction(ctx, req.OnBehalf, http.MethodPost, dingApprovalCreatePath, nil, body, &out); err != nil {
		return contract.ExecReceipt{}, err
	}
	if out.InstanceID == "" {
		return contract.ExecReceipt{}, contract.ExecErr(contract.ExecKindPlatform, "", "钉钉审批创建：200 但缺实例号", false)
	}
	return contract.ExecReceipt{Ref: out.InstanceID, Message: "审批已提交（单号 " + out.InstanceID + "）"}, nil
}

// formValues 表单组件值行（排除 type/person 两个路由键；键序确定性——排序）。
func formValues(params map[string]string) []dingFormValue {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "type" || k == "person" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	vals := make([]dingFormValue, 0, len(keys))
	for _, k := range keys {
		vals = append(vals, dingFormValue{Name: k, Value: params[k]})
	}
	return vals
}

// execQuery 查审批进度：状态枚举归一人话，未知枚举原文透出（不猜）。
func (a *dingtalkAction) execQuery(ctx context.Context, req contract.ExecRequest) (contract.ExecReceipt, error) {
	var out struct {
		Title  string `json:"title"`
		Status string `json:"status"` // NEW|RUNNING|COMPLETED|TERMINATED
		Result string `json:"result"` // agree|refuse（COMPLETED 时有意义）
	}
	if err := a.callAction(ctx, req.OnBehalf, http.MethodGet, fmt.Sprintf(dingApprovalGetFmt, req.Params["ref"]), nil, nil, &out); err != nil {
		return contract.ExecReceipt{}, err
	}
	text := approvalStatusText(out.Status, out.Result)
	if text == "" {
		return contract.ExecReceipt{}, contract.ExecErr(contract.ExecKindPlatform, "", "钉钉审批查询：200 但缺状态", false)
	}
	return contract.ExecReceipt{Ref: req.Params["ref"], Message: "审批状态：" + text}, nil
}

// approvalStatusText 钉钉实例状态枚举 → 人话（未知枚举原文透出——空 = 平台未回）。
func approvalStatusText(status, result string) string {
	switch status {
	case "NEW":
		return "已创建"
	case "RUNNING":
		return "审批中"
	case "COMPLETED":
		switch result {
		case "agree":
			return "已完成（已同意）"
		case "refuse":
			return "已完成（已拒绝）"
		default:
			return "已完成"
		}
	case "TERMINATED":
		return "已终止"
	default:
		return status // 未知枚举原文（含空串 = 调用方判缺）
	}
}

// execCreateTodo 创建待办：unionId 口径执行人 + 标题 + 截止（可选）。
func (a *dingtalkAction) execCreateTodo(ctx context.Context, req contract.ExecRequest) (contract.ExecReceipt, error) {
	body := map[string]any{
		"unionId": req.Params["person"], // 钉钉 unionId 口径（宿主绑定键映射——真租户校准点挂仓 issue）
		"subject": req.Params["title"],
	}
	if due := req.Params["due"]; due != "" {
		ms, err := parseDueMs(due)
		if err != nil {
			return contract.ExecReceipt{}, contract.ExecErr(contract.ExecKindInvalidParams, "", "due 参数不可解析（RFC3339 或 unix 秒）："+due, false)
		}
		body["dueTime"] = ms // 毫秒口径
	}
	var out struct {
		TaskID string `json:"taskId"` // 待办 id = 追溯 Ref
	}
	if err := a.callAction(ctx, req.OnBehalf, http.MethodPost, dingTodoCreatePath, nil, body, &out); err != nil {
		return contract.ExecReceipt{}, err
	}
	if out.TaskID == "" {
		return contract.ExecReceipt{}, contract.ExecErr(contract.ExecKindPlatform, "", "钉钉待办创建：200 但缺待办号", false)
	}
	return contract.ExecReceipt{Ref: out.TaskID, Message: "待办已创建（" + req.Params["title"] + "）"}, nil
}

// parseDueMs 截止参数 → 毫秒（RFC3339 或 unix 秒两种口径；空 = 0 不设）。
func parseDueMs(s string) (int64, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UnixMilli(), nil
	}
	if sec, err := strconv.ParseInt(s, 10, 64); err == nil {
		return sec * 1000, nil
	}
	return 0, fmt.Errorf("不可解析: %q", s)
}

// callAction 操作面统一出口：OnBehalf 非空 = 发起人个人令牌直发（不缓存不
// 刷新——令牌归宿主）；空 = 应用 accessToken（失效刷新重试一次）。失败语义
// 归类（可辨不吞）。
func (a *dingtalkAction) callAction(ctx context.Context, onBehalf, method, path string, query url.Values, body, out any) error {
	if onBehalf != "" {
		return classifyExecError(a.core.do(ctx, method, path, query, body, out, onBehalf))
	}
	if err := a.core.ensureToken(ctx); err != nil {
		return contract.ExecErr(contract.ExecKindUnauthorized, "", "钉钉应用令牌获取失败："+err.Error(), false)
	}
	err := a.core.do(ctx, method, path, query, body, out, a.core.token)
	if err != nil && isTokenStale(err) {
		a.core.token, a.core.expiry = "", time.Time{}
		if err2 := a.core.ensureToken(ctx); err2 != nil {
			return contract.ExecErr(contract.ExecKindUnauthorized, "", "钉钉应用令牌刷新失败："+err2.Error(), false)
		}
		err = a.core.do(ctx, method, path, query, body, out, a.core.token)
	}
	return classifyExecError(err)
}

// classifyExecError 失败语义归类（传输/http 两层——不吞不改写：平台原文进
// Message，Kind 供宿主分支回执与幂等重试）。
func classifyExecError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "http 401"):
		return contract.ExecErr(contract.ExecKindUnauthorized, "", msg, false)
	case strings.Contains(msg, "http 403"):
		return contract.ExecErr(contract.ExecKindForbidden, "", msg, false)
	default: // 传输/网关故障：可重试（宿主同 IdempotencyKey 重试）
		return contract.ExecErr(contract.ExecKindPlatform, "", msg, true)
	}
}
