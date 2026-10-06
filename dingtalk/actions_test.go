// 操作能力声明自洽单测（批次①）：系统键与同步面 Platform 同源、五键齐、
// 操作键不重复、必填参数键/标签非空。跨仓对偶（与宿主操作目录逐行对账）
// 在宿主侧对偶闸测试收口——本测试只管本包声明面自身不撒谎。
package dingtalk

import (
	"testing"

	"github.com/tunsuy/agthelm-connectors/contract"
)

func TestActionCapabilitiesSelfConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range ActionCapabilities {
		if c.System != dingtalkPlatform {
			t.Errorf("能力 %s 系统键 %q 与平台常量 %q 不一致", c.Action, c.System, dingtalkPlatform)
		}
		if c.SystemLabel == "" || c.Action == "" || c.ActionLabel == "" {
			t.Errorf("能力 %+v 五键不全（SystemLabel/Action/ActionLabel）", c)
		}
		if seen[c.Action] {
			t.Errorf("操作键 %q 重复声明", c.Action)
		}
		seen[c.Action] = true
		for _, p := range c.Params {
			if p.Key == "" || p.Label == "" {
				t.Errorf("能力 %s 参数 %+v 键或标签为空", c.Action, p)
			}
		}
	}
	if len(ActionCapabilities) == 0 {
		t.Fatal("操作能力清单为空——打样系统应声明审批/待办面能力")
	}
}

// 能力清单必须可被宿主按 contract.ActionCapability 消费（类型面钉死——
// 改形不破坏对偶闸的编译期哨兵）。
var _ []contract.ActionCapability = ActionCapabilities
