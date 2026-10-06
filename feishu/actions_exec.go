// 飞书操作面（v0.3 增补，additive）：发起审批 / 查审批进度。复用同步面客户端
// 骨架（同一 beeo 信封口径 + tenant token 缓存与失效刷新重试）；OnBehalf 非空 =
// 宿主已取的发起人用户令牌（Bearer 直发，不缓存不刷新——令牌归宿主；失效 =
// unauthorized 诚实回执，由宿主指引补授权），空 = 应用身份 tenant_access_token。
// 端点形状按公开文档钉死（真租户校准点挂仓 issue）；form 组件形状为参数键值
// 字符串化 JSON（同上挂 issue）。IdempotencyKey 平台端点无幂等参数——宿主侧
// 同键去重，adapter 不消费。
package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tunsuy/agthelm-connectors/contract"
)

const (
	feishuApprovalCreatePath = "/open-apis/approval/v4/instances"
	feishuApprovalGetFmt     = "/open-apis/approval/v4/instances/%s"
)

// 失败语义归类码表（真租户校准点挂仓 issue——按实测错误码补行）：token 失效
// 族 → unauthorized；应用/用户权限不足族 → forbidden（ADR 口径：不吞不改写）。
var (
	feishuStaleCodes  = map[int]bool{99991661: true, 99991663: true, 99991668: true}
	feishuDeniedCodes = map[int]bool{99991672: true, 99991679: true}
)

// FeishuActionConfig 操作面配置（BaseURL 可注入 = fake 剑本消费同一实现）。
type FeishuActionConfig struct {
	BaseURL   string
	AppID     string // 应用凭据（与同步面同键族——宿主凭据面归一管理）
	AppSecret string
	// ApprovalCodes 审批类型键 → 租户审批定义 code 映射（飞书后台建审批流后的
	// 码；缺映射 = 类型键原文直用——真租户校准点挂仓 issue）。
	ApprovalCodes map[string]string
}

// NewFeishuAction 构造操作面连接器（cfg 校验失败 → error，接线判定归调用方）。
func NewFeishuAction(cfg FeishuActionConfig) (contract.ActionConnector, error) {
	if cfg.BaseURL == "" || cfg.AppID == "" || cfg.AppSecret == "" {
		return nil, fmt.Errorf("feishu action: 配置不完整（BaseURL/AppID/AppSecret）")
	}
	return &feishuAction{
		core: feishuClient{
			cfg: FeishuConfig{BaseURL: cfg.BaseURL, AppID: cfg.AppID, AppSecret: cfg.AppSecret},
			hc:  &http.Client{Timeout: 30 * time.Second},
		},
		codes: cfg.ApprovalCodes,
	}, nil
}

// feishuAction 操作面连接器（复用 feishuClient 的 do/ensureToken 骨架）。
type feishuAction struct {
	core  feishuClient
	codes map[string]string
}

// 编译哨兵：操作面实现必须满足 v0.2 契约（漂移 = 编译失败）。
var _ contract.ActionConnector = (*feishuAction)(nil)

func (a *feishuAction) System() string                            { return feishuPlatform }
func (a *feishuAction) Capabilities() []contract.ActionCapability { return ActionCapabilities }
func (a *feishuAction) Exec(ctx context.Context, req contract.ExecRequest) (contract.ExecReceipt, error) {
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

// execSubmit 发起审批：approval_code（映射或类型键原文）+ 发起人 + 表单组件值。
func (a *feishuAction) execSubmit(ctx context.Context, req contract.ExecRequest) (contract.ExecReceipt, error) {
	typ := req.Params["type"]
	code := a.codes[typ]
	if code == "" {
		code = typ
	}
	form, err := formComponents(req.Params)
	if err != nil {
		return contract.ExecReceipt{}, contract.ExecErr(contract.ExecKindInvalidParams, "", "表单组件构造失败："+err.Error(), false)
	}
	body := map[string]any{
		"approval_code": code,
		"user_id":       req.Params["person"], // 发起人（user_id_type=open_id 口径）
		"form":          string(form),
	}
	var out struct {
		InstanceCode string `json:"instance_code"` // 实例号 = 追溯 Ref（宿主幂等键锚点）
	}
	q := url.Values{"user_id_type": {"open_id"}}
	if err := a.callAction(ctx, req.OnBehalf, http.MethodPost, feishuApprovalCreatePath, q, body, &out); err != nil {
		return contract.ExecReceipt{}, err
	}
	if out.InstanceCode == "" {
		return contract.ExecReceipt{}, contract.ExecErr(contract.ExecKindPlatform, "", "飞书审批创建：code=0 但缺实例号", false)
	}
	return contract.ExecReceipt{Ref: out.InstanceCode, Message: "审批已提交（单号 " + out.InstanceCode + "）"}, nil
}

// formComponents 表单组件值（参数键值字符串化 JSON——排除 type/person 两个
// 路由键；组件形状真租户校准点挂仓 issue）。
func formComponents(params map[string]string) ([]byte, error) {
	vals := map[string]string{}
	for k, v := range params {
		if k == "type" || k == "person" {
			continue
		}
		vals[k] = v
	}
	return json.Marshal(vals)
}

// execQuery 查审批进度：状态枚举归一人话，未知枚举原文透出（不猜）。
func (a *feishuAction) execQuery(ctx context.Context, req contract.ExecRequest) (contract.ExecReceipt, error) {
	var out struct {
		Instance struct {
			ApprovalCode string `json:"approval_code"`
			Status       string `json:"status"` // PENDING|APPROVED|REJECTED|CANCELED|DELETED
		} `json:"instance"`
	}
	q := url.Values{"user_id_type": {"open_id"}}
	if err := a.callAction(ctx, req.OnBehalf, http.MethodGet, fmt.Sprintf(feishuApprovalGetFmt, req.Params["ref"]), q, nil, &out); err != nil {
		return contract.ExecReceipt{}, err
	}
	status := approvalStatusText(out.Instance.Status)
	if status == "" {
		return contract.ExecReceipt{}, contract.ExecErr(contract.ExecKindPlatform, "", "飞书审批查询：code=0 但缺状态", false)
	}
	return contract.ExecReceipt{Ref: req.Params["ref"], Message: "审批状态：" + status}, nil
}

// approvalStatusText 飞书实例状态枚举 → 人话（未知枚举原文透出——空 = 平台未回）。
func approvalStatusText(s string) string {
	switch s {
	case "PENDING":
		return "审批中"
	case "APPROVED":
		return "已通过"
	case "REJECTED":
		return "已拒绝"
	case "CANCELED":
		return "已撤销"
	case "DELETED":
		return "已删除"
	default:
		return s // 未知枚举原文（含空串 = 调用方判缺）
	}
}

// callAction 操作面统一出口：OnBehalf 非空 = 发起人用户令牌直发（不缓存不刷新
// ——令牌归宿主）；空 = 应用 tenant token（失效刷新重试一次）。失败语义归类。
func (a *feishuAction) callAction(ctx context.Context, onBehalf, method, path string, query url.Values, body, out any) error {
	if onBehalf != "" {
		env, err := a.core.do(ctx, method, path, query, body, out, onBehalf)
		return classifyExecError(err, env)
	}
	if err := a.core.ensureToken(ctx); err != nil {
		return contract.ExecErr(contract.ExecKindUnauthorized, "", "飞书应用令牌获取失败："+err.Error(), false)
	}
	env, err := a.core.do(ctx, method, path, query, body, out, a.core.token)
	if err == nil && env != nil && feishuStaleCodes[env.Code] {
		a.core.token, a.core.expiry = "", time.Time{}
		if err2 := a.core.ensureToken(ctx); err2 != nil {
			return contract.ExecErr(contract.ExecKindUnauthorized, "", "飞书应用令牌刷新失败："+err2.Error(), false)
		}
		env, err = a.core.do(ctx, method, path, query, body, out, a.core.token)
	}
	return classifyExecError(err, env)
}

// classifyExecError 失败语义归类（传输/http/信封三层——不吞不改写：平台原文
// 进 Message，Kind 供宿主分支回执与幂等重试）。
func classifyExecError(err error, env *beeoEnvelope) error {
	if err != nil {
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
	if env == nil || env.Code == 0 {
		return nil
	}
	code := fmt.Sprintf("%d", env.Code)
	switch {
	case feishuStaleCodes[env.Code]:
		return contract.ExecErr(contract.ExecKindUnauthorized, code, env.Msg, false)
	case feishuDeniedCodes[env.Code]:
		return contract.ExecErr(contract.ExecKindForbidden, code, env.Msg, false)
	default: // 业务失败（未知码）：不自动重试——人工看平台原文
		return contract.ExecErr(contract.ExecKindPlatform, code, env.Msg, false)
	}
}
