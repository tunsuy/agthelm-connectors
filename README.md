# agthelm-connectors

知识源连接器开源共建仓——为 [agthelm](https://github.com/tunsuy)（企业 AI 转型底座）及任何需要「从协作平台拉文档/权限/通讯录」的系统提供社区共建的连接器集合。

Apache-2.0 · Go module `github.com/tunsuy/agthelm-connectors`

## 为什么单独一个仓

知识源连接器是典型的**长尾生态**：飞书/钉钉只是开头，Confluence/SharePoint/企微/Notion/语雀/S3 永远追不完。与其等一个团队排期，不如让社区按同一契约贡献。

## 仓里有什么 / 没有什么

| 在本仓 | 不在本仓（归宿主私有侧） |
|---|---|
| `contract/`：版本化三段契约（`ContractVersion`）+ 中立 DTO | 同步引擎（幂等收敛/水位/runs 报表） |
| `feishu/`：飞书 adapter（云文档 + 知识库五面） | 落库与事务（pg/存储端口） |
| `dingtalk/`：钉钉 adapter（v1.0 + topapi 双信封） | 身份/权限/治理语义（主体展开、ACL 收敛） |
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

## 新增一个 adapter（最小路径）

1. 读 `contract/connector.go`（五方法 + 纪律）和 `contract/types.go`（四 DTO）；
2. 照 `feishu/` 或 `dingtalk/` 的剑本模式新建 `<platform>/` 包：
   - 端点字符串全部收在本包 const 块（单点可校准）；
   - token 缓存 + 失效刷新重试一次的骨架可直接复用两 adapter 的 `call/do/ensureToken` 形状；
   - 配置结构带 `BaseURL` 注入口（fake 剑本消费同一实现——见下）；
3. **必须带剑本测试**：httptest fake 复刻平台端点信封形状（参考 `feishu/feishu_test.go`）——`t.Errorf("未知端点")` 分支保证协议漂移在 CI 爆出；
4. 本地 `go build ./... && go vet ./... && go test ./...` 全绿后提 PR。

贡献模式 phase 1 = in-tree PR（无动态加载/插件机制）。详见 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 许可

Apache-2.0（见 [LICENSE](LICENSE)）；归属说明见 [NOTICE](NOTICE)。feishu/dingtalk 两 adapter 源自 agthelm 批次 5（ADR-008 拆仓）。
