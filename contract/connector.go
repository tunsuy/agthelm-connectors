// Package contract 定义知识源连接器契约（v1）。
//
// 这是 agthelm-connectors 公仓与宿主（agthelm）之间的唯一耦合面：
// adapter 只依赖本包（零宿主 import——依赖方向铁律：私有 import 公开，
// 永不反向）；宿主通过自己的薄适配层消费本契约。
//
// 契约纪律（ContractVersion 语义）：
//   - 新增方法/字段 = 兼容演进（minor tag）；
//   - 破坏性变更（删改方法/字段语义翻转）= ContractVersion +1 + major tag，
//     宿主编译期锁版暴露漂移。
//
// 三段式形状（Load/Poll/PermSync 借鉴 Onyx 接口形状，代码自研）：
// adapter 只产数据批次，不落库——落库、事务收敛、身份/权限语义全部归宿主。
package contract

import "context"

// ContractVersion 是契约版本（破坏性变更时 +1，配公仓 major tag）。
const ContractVersion = 1

// Connector 是三段式知识源连接器端口。
//
// 实现纪律：
//   - 幂等友好：Load/Poll 重放恒等（同一可见状态产同一批次）；
//   - 源端命名空间：所有 ID/成员键用源平台的标识（open_id/source_dept_id），
//     不得引入宿主身份概念；
//   - 失败上抛：业务失败/传输失败统一 error 出口，重试与水位归宿主；
//   - 端点单点：平台 API 端点字符串只准出现在本 adapter 包内
//     （换源/协议校准单点改一个文件）。
type Connector interface {
	// Platform 平台名（宿主侧表/状态/绑定行的 platform 列值；如 feishu|dingtalk）。
	Platform() string
	// LoadDocs 全量拉取（首轮：宿主侧游标为空时）。
	LoadDocs(ctx context.Context) ([]Document, error)
	// PollDocs 增量轮询：返回当前可见全集（缺席 = 删除事件，由宿主 diff 收敛），
	// 并返回新游标（语义 = 源端水位，供追溯与下次轮起点）。
	PollDocs(ctx context.Context, cursor string) ([]Document, string, error)
	// LoadACLs 全量权限元数据（宿主 replace 收敛的输入面，源端命名空间）。
	LoadACLs(ctx context.Context) ([]DocACL, error)
	// LoadDirectory 全量通讯录 + 源端部门树（宿主侧身份映射/部门解析物料）。
	LoadDirectory(ctx context.Context) ([]IMContact, []SourceDept, error)
}

// 护栏缺省值（adapter 侧共享；MaxDocBytes 可被配置覆盖，其余为实现护栏）。
const (
	// DefaultMaxDocBytes 单文档正文默认护栏（超限只保 metadata，Oversize=true）。
	DefaultMaxDocBytes = int64(10 << 20)
	// MaxResponseBytes 单响应体读上限（信封 + 正文富余）。
	MaxResponseBytes = int64(64 << 20)
	// DefaultPageSize 分页缺省页大小。
	DefaultPageSize = 50
)
