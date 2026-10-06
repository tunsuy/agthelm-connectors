// 操作型执行的共享小件（v0.3 增补，additive）：可辨失败语义 ExecError + 参数
// 必填校验 ValidateParams。宿主侧 AC 口径：forbidden = 发起人无权限（透传
// 不假扮成功）；unauthorized = 令牌无效/过期（指引补授权）；invalid_params =
// 参数缺失（不猜默认值）；platform = 平台侧故障/未知（Retryable 归类）。
package contract

import (
	"fmt"
	"sort"
	"strings"
)

// ExecError 失败语义分类（adapter 对平台错误归类；宿主按 Kind 分支回执与
// 幂等重试——失败语义可辨，不吞不改写）。
type ExecError struct {
	// Kind 失败类别（ExecKind* 常量）。
	Kind string
	// Code 平台侧错误码原文（可空——传输层失败无码）。
	Code string
	// Message 平台侧错误信息原文（宿主透传时可拼接，不改写语义）。
	Message string
	// Retryable 幂等重试判定（true = 宿主可安全重试同 IdempotencyKey）。
	Retryable bool
}

func (e *ExecError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("exec %s: code=%s msg=%s", e.Kind, e.Code, e.Message)
	}
	return fmt.Sprintf("exec %s: msg=%s", e.Kind, e.Message)
}

// 失败类别常量（中立语义——零宿主概念）。
const (
	ExecKindUnauthorized  = "unauthorized"   // 令牌无效/过期
	ExecKindForbidden     = "forbidden"      // 发起人无权限
	ExecKindInvalidParams = "invalid_params" // 参数缺失/非法
	ExecKindNotFound      = "not_found"      // 单据/记录不存在
	ExecKindPlatform      = "platform"       // 平台故障/未知错误
)

// ExecErr 快构（msg 人话可读；retryable 只对 platform 类有语义）。
func ExecErr(kind, code, msg string, retryable bool) *ExecError {
	return &ExecError{Kind: kind, Code: code, Message: msg, Retryable: retryable}
}

// ValidateParams 按能力声明校验必填参数（缺 = invalid_params——逐项报齐不短路，
// 员工一次看全要补什么）。键序确定性（排序后遍历）。
func ValidateParams(cap ActionCapability, params map[string]string) error {
	var missing []string
	for _, p := range cap.Params {
		if !p.Required {
			continue
		}
		if strings.TrimSpace(params[p.Key]) == "" {
			missing = append(missing, p.Label)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return ExecErr(ExecKindInvalidParams, "", "缺少必填参数："+strings.Join(missing, "、"), false)
}
