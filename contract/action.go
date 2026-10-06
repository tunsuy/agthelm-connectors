// 操作型契约（v0.2 增补，additive——ContractVersion 不动：新增接口/字段 = 兼容
// 演进，宿主旧消费面零破坏）。
//
// 同步面（Connector）拉知识，操作面（ActionConnector）办事：提交审批、
// 查进度、建待办——由宿主的确认闸与白名单罩住后真实执行。同一 adapter 包
// 可只实现同步面、只实现操作面，或两者（宿主按面装配，互不强求）。
//
// 键口径对偶：ActionCapability.System/Action 与宿主侧操作白名单行双键
// （system_key/action_key）逐字对偶——宿主启动期对偶校验消费（键不齐 =
// 构建期/测试期失败，两侧同改纪律机械化）。
package contract

import "context"

// ActionParam 操作参数声明（人话标签 + 必填——宿主提示词注入与参数校验共享；
// 值一律字符串，结构化参数由 adapter 侧解析并诚实报错）。
type ActionParam struct {
	// Key 参数键（宿主执行请求 Params 的键）。
	Key string
	// Label 员工可读参数名（如「审批类型」）。
	Label string
	// Required 必填（缺 = adapter 诚实失败，不猜默认值）。
	Required bool
}

// ActionCapability 操作能力行（adapter 声明面：「本系统键下能执行哪些操作」）。
// 五键自描述（System/SystemLabel/Action/ActionLabel/Write）与模板动作段
// 条目级载荷同构——清单可原样流通，消费者不依赖宿主目录也能读懂。
type ActionCapability struct {
	System      string
	SystemLabel string
	Action      string
	ActionLabel string
	// Write 改数据（执行前必须发起人确认——宿主确认闸消费）。
	Write bool
	// Params 参数清单（声明面；执行期按必填校验）。
	Params []ActionParam
}

// ExecRequest 执行请求（中立 DTO：操作键 + 参数 + 凭据句柄——零宿主身份概念）。
type ExecRequest struct {
	// Action 操作键（Capabilities 声明面内的键；未声明键由宿主白名单前置拦截）。
	Action string
	// Params 参数（键 = ActionParam.Key；缺必填 = 诚实失败）。
	Params map[string]string
	// OnBehalf 发起人平台令牌（源端命名空间的不透明句柄——宿主以发起人本人
	// 身份换取后传入；空 = 应用凭据形态。用哪种身份归宿主部署期配置，本契约
	// 不仲裁、不解释令牌内容）。
	OnBehalf string
	// IdempotencyKey 宿主幂等键（同一确认防双发；平台防重字段可用则透传）。
	IdempotencyKey string
}

// ExecReceipt 执行回执（成功面；失败统一 error 出口——同同步面契约纪律，
// 失败语义上抛不吞，重试归宿主）。
type ExecReceipt struct {
	// Ref 平台侧单据号/记录键（审批单号等——宿主回执、审计与幂等对账引用）。
	Ref string
	// Message 人话回执摘要（员工可见，如「审批单已提交」）。
	Message string
}

// ActionConnector 操作型连接器端口。
//
// 实现纪律（同同步面）：源端命名空间（零宿主身份概念）；失败上抛（错误
// 语义透传——权限不足/参数缺失/平台故障各自可辨，不吞不改写）；幂等
// （同 IdempotencyKey 重放不双发——平台无防重字段时由返回 Ref 幂等收敛）。
type ActionConnector interface {
	// System 系统键（与 ActionCapability.System 及宿主白名单行 system 键对偶）。
	System() string
	// Capabilities 能力清单（静态声明面——宿主启动期对偶校验消费）。
	Capabilities() []ActionCapability
	// Exec 执行一次操作（写操作已由宿主确认闸放行；本方法不二次确认）。
	Exec(ctx context.Context, req ExecRequest) (ExecReceipt, error)
}
