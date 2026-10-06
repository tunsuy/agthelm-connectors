// 飞书操作能力声明（操作型面批次①：契约对偶 SSOT——宿主操作目录逐行对偶
// 校验消费；Exec 实现 = 批次②真实 API 客户端）。
// 打样面 = 审批（飞书审批）：发起 + 查进度。端点形状届时单点钉在
// feishu.go 端点区（与同步面同纪律）。
package feishu

import "github.com/tunsuy/agthelm-connectors/contract"

// ActionCapabilities 飞书操作能力清单（宿主侧操作目录 feishu 系统行的
// 对偶源——键/标签/写分级两侧同改）。
var ActionCapabilities = []contract.ActionCapability{
	{
		System: feishuPlatform, SystemLabel: "飞书",
		Action: "submit_approval", ActionLabel: "发起审批", Write: true,
		Params: []contract.ActionParam{
			{Key: "type", Label: "审批类型", Required: true},
			{Key: "person", Label: "申请人", Required: true},
			{Key: "date", Label: "日期", Required: false},
			{Key: "reason", Label: "事由", Required: false},
		},
	},
	{
		System: feishuPlatform, SystemLabel: "飞书",
		Action: "query_approval", ActionLabel: "查审批进度", Write: false,
		Params: []contract.ActionParam{
			{Key: "ref", Label: "审批单号", Required: true},
		},
	},
}
