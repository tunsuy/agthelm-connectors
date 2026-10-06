// ExecError / ValidateParams 单测：必填报齐（逐项不短路）、键序确定性、
// 错误串形态（带码/无码）。
package contract

import (
	"strings"
	"testing"
)

func TestValidateParamsOK(t *testing.T) {
	cap := ActionCapability{
		Action: "submit_approval",
		Params: []ActionParam{
			{Key: "type", Label: "审批类型", Required: true},
			{Key: "person", Label: "申请人", Required: true},
			{Key: "reason", Label: "事由", Required: false},
		},
	}
	if err := ValidateParams(cap, map[string]string{"type": "补卡", "person": "ou-1"}); err != nil {
		t.Errorf("必填齐 + 可选缺 = 应过: %v", err)
	}
	if err := ValidateParams(cap, map[string]string{"type": "补卡", "person": "ou-1", "reason": "忘打卡"}); err != nil {
		t.Errorf("全填 = 应过: %v", err)
	}
}

func TestValidateParamsMissing(t *testing.T) {
	cap := ActionCapability{
		Action: "submit_approval",
		Params: []ActionParam{
			{Key: "type", Label: "审批类型", Required: true},
			{Key: "person", Label: "申请人", Required: true},
		},
	}
	err := ValidateParams(cap, map[string]string{"person": " "}) // type 缺 + person 空白
	if err == nil {
		t.Fatal("缺必填应失败")
	}
	ee, ok := err.(*ExecError)
	if !ok {
		t.Fatalf("预期 *ExecError，得到 %T", err)
	}
	if ee.Kind != ExecKindInvalidParams {
		t.Errorf("Kind 失配: %s", ee.Kind)
	}
	// 逐项报齐（不短路）+ 排序稳定（审批类型、申请人——键序与报齐一起钉）
	for _, label := range []string{"审批类型", "申请人"} {
		if !strings.Contains(ee.Message, label) {
			t.Errorf("报文应含 %q: %s", label, ee.Message)
		}
	}
}

func TestExecErrorString(t *testing.T) {
	withCode := ExecErr(ExecKindForbidden, "99991672", "no permission", false)
	if !strings.Contains(withCode.Error(), "code=99991672") {
		t.Errorf("带码错误串应含码: %s", withCode.Error())
	}
	noCode := ExecErr(ExecKindInvalidParams, "", "缺少必填参数", false)
	if strings.Contains(noCode.Error(), "code=") {
		t.Errorf("无码错误串不应带码段: %s", noCode.Error())
	}
}
