# agthelm-connectors

知识源连接器开源共建仓——为 [agthelm](https://github.com/tunsuy)（企业 AI 转型底座）及任何需要「从协作平台拉文档/权限/通讯录」的系统提供社区共建的连接器集合。

Apache-2.0 · Go module `github.com/tunsuy/agthelm-connectors`

## 为什么单独一个仓

知识源连接器是典型的**长尾生态**：飞书/钉钉只是开头，Confluence/SharePoint/企微/Notion/语雀/S3 永远追不完。与其等一个团队排期，不如让社区按同一契约贡献。

## 仓里有什么 / 没有什么

| 在本仓 | 不在本仓（归宿主私有侧） |
|---|---|
| `contract/`：版本化契约（`ContractVersion`）+ 中立 DTO——同步面（三段式）与操作面（能力清单 + Exec） | 同步引擎（幂等收敛/水位/runs 报表） |
| `feishu/`：飞书 adapter（同步五面 + 操作能力声明：审批） | 落库与事务（pg/存储端口） |
| `dingtalk/`：钉钉 adapter（同步五面 + 操作能力声明：审批/待办） | 身份/权限/治理语义（主体展开、ACL 收敛、确认闸） |
| 剑本测试（httptest fake 钉协议信封形状） | 控制台装配与观测端点 |

**依赖方向铁律：adapter 只依赖 `contract` 包（零宿主 import）。** 宿主通过自己的薄适配层消费本仓（私有 import 公开，永不反向）。

## 契约一览（v1）

```go
type Connector interface {
    Platform() string
    LoadDocs(ctx) ([]Document, error)                // 全量（首轮）
    PollDocs(ctx, cursor) ([]Document, string, error) // 增量（缺席 = 删除事件）
    LoadACLs(ctx) ([]DocACL, error)                   // 源端权限元数据（replace 收敛输入）
    LoadDirectory(ctx) ([]IMContact, []SourceDept, error) // 通讯录 + 部门树
}
```

实现纪律：幂等友好（重放恒等）· 源端命名空间（open_id/source_dept_id，零宿主身份概念）· 失败上抛（重试/水位归宿主）· 端点字符串单点在本包内。

### 操作面（v0.2 增补，additive）

```go
type ActionConnector interface {
    System() string                       // 系统键（与宿主操作白名单行对偶）
    Capabilities() []ActionCapability     // 能力清单：system/action 双键 + 标签 + 写分级 + 参数
    Exec(ctx, ExecRequest) (ExecReceipt, error) // 真实执行（凭据句柄在请求上，身份语义归宿主）
}
```

- 能力清单是 adapter 的**声明面**：五键自描述（system/systemLabel/action/actionLabel/write，与 agthelm 模板动作段载荷同构），宿主启动期与自己的操作目录逐行对偶——键不齐 = 构建期/测试期失败。
- `ExecRequest` 携带操作键 + 参数 + **凭据句柄**（`OnBehalf` = 发起人平台令牌的不透明字符串，空 = 应用凭据形态——用哪种身份归宿主部署期配置，契约不仲裁）+ 宿主幂等键（平台端点无幂等参数，同键去重归宿主，adapter 不消费）。
- 失败统一 error 出口（v0.3 起为 `*contract.ExecError`：`Kind` ∈ unauthorized/forbidden/invalid_params/not_found/platform + 平台码原文 + `Retryable` 幂等重试判定），透传不吞；回执 `Ref` = 平台侧单据号供审计对账。
- Exec 实现已落地打样：`feishu.NewFeishuAction` / `dingtalk.NewDingTalkAction`（审批创建/查询 + 钉钉待办；`OnBehalf` 非空 = 发起人令牌直发不缓存不刷新，空 = 应用 token 带失效重试）。端点形状按公开文档钉死，真租户校准点挂仓 issue。

## 使用

```go
import (
    "github.com/tunsuy/agthelm-connectors/contract"
    "github.com/tunsuy/agthelm-connectors/feishu"
)

conn, err := feishu.NewFeishu(feishu.FeishuConfig{
    BaseURL: "https://open.feishu.cn", // 配置面承载域名（本仓源码零厂商域名）
    AppID:   os.Getenv("APP_ID"),
    AppSecret: os.Getenv("APP_SECRET"),
})
var c contract.Connector = conn
docs, err := c.LoadDocs(ctx)
```

操作面（v0.3）：

```go
act, err := feishu.NewFeishuAction(feishu.FeishuActionConfig{
    BaseURL: "https://open.feishu.cn",
    AppID:   os.Getenv("APP_ID"), AppSecret: os.Getenv("APP_SECRET"),
    ApprovalCodes: map[string]string{"补卡": "<租户审批定义 code>"}, // 缺映射 = 类型键原文直用
})
rc, err := act.Exec(ctx, contract.ExecRequest{
    Action:   "submit_approval",
    Params:   map[string]string{"type": "补卡", "person": "ou-…"},
    OnBehalf: "<发起人用户令牌；空 = 应用身份>",
})
// 失败 = *contract.ExecError（Kind 可辨：unauthorized/forbidden/invalid_params/platform…）
```

## 新增一个 adapter（最小路径）

1. 读 `contract/connector.go`（五方法 + 纪律）和 `contract/types.go`（四 DTO）；
2. 照 `feishu/` 或 `dingtalk/` 的剑本模式新建 `<platform>/` 包：
   - 端点字符串全部收在本包 const 块（单点可校准）；
   - token 缓存 + 失效刷新重试一次的骨架可直接复用两 adapter 的 `call/do/ensureToken` 形状；
   - 配置结构带 `BaseURL` 注入口（fake 剑本消费同一实现——见下）；
3. 可选操作面：`<platform>/actions.go` 声明 `ActionCapabilities`（键/标签/写分级 + 参数——宿主操作目录对偶源），Exec 实现随后补（参考 `feishu/actions_exec.go`：能力查行 → `contract.ValidateParams` 必填校验 → 分发执行 → 失败语义归类 `*contract.ExecError`）；
4. **必须带剑本测试**：httptest fake 复刻平台端点信封形状（参考 `feishu/feishu_test.go`）——`t.Errorf("未知端点")` 分支保证协议漂移在 CI 爆出；
5. 本地 `go build ./... && go vet ./... && go test ./...` 全绿后提 PR。

贡献模式 phase 1 = in-tree PR（无动态加载/插件机制）。详见 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 许可

Apache-2.0（见 [LICENSE](LICENSE)）；归属说明见 [NOTICE](NOTICE)。feishu/dingtalk 两 adapter 源自 agthelm 批次 5（ADR-008 拆仓）。
