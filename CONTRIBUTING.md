# 贡献指南

感谢为 agthelm-connectors 贡献连接器——每一个新 adapter 都在帮「长尾知识源」往前追一步。

## 贡献模式（phase 1：in-tree PR）

- 新 adapter = 仓库内 `<platform>/` 目录进树 PR，**不做**动态加载/插件机制（宿主编译期锁版，漂移在构建期暴露）；
- 契约演进遵守 `contract.ContractVersion` 语义：加方法/字段 = 兼容（minor）；破坏性变更 = +1 + major tag，须先开 issue 讨论。

## 一个合格 adapter 的清单

1. **只依赖 `contract` 包**——零宿主/其他 adapter import（依赖方向铁律）；
2. **五方法全实现**：`Platform/LoadDocs/PollDocs/LoadACLs/LoadDirectory`；
3. **端点字符串单点**：平台 API 路径全部收在本包 const 块，源码零厂商域名（BaseURL 从配置注入）；
4. **token 生命周期**：缓存 + 提前过期 + 失效刷新重试一次（照抄 feishu/dingtalk 的 `call/do/ensureToken` 骨架形状即可）；
5. **护栏**：`MaxDocBytes`（超限置 `Oversize=true` 只保 metadata）+ `contract.MaxResponseBytes` 读上限；
6. **剑本测试（硬性要求）**：httptest fake 复刻端点信封形状，未知端点分支 `t.Errorf` ——协议漂移必须在 CI 爆出，不许静默吞。最小覆盖面参照 `feishu/feishu_test.go`：配置校验 / 全链 pipeline / token 失效重试 / oversize 护栏 / 业务失败上抛；
7. **注释口径**：真租户校准点（实测后才能钉死的形状细节）挂 issue 引用，不写死猜测。

## PR 流程

1. fork → 分支 → 实现 + 测试；
2. `go build ./... && go vet ./... && go test ./...` 本地全绿；
3. PR 描述写明：平台公开文档链接、剑本覆盖的端点面、已知校准点（哪些形状待真租户实测）；
4. CI（build/vet/test）绿 + review 通过即合入。

## 行为准则

保持专业与尊重；技术分歧以契约纪律和测试证据为准绳。
