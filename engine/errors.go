package engine

// Reason 标识配置或求值被拒绝的可区分原因。
type Reason string

const (
	ReasonSourceMissing     Reason = "SOURCE_MISSING"     // 配置来源缺失或不可读
	ReasonTypeMismatch      Reason = "TYPE_MISMATCH"      // 配置值类型与声明不符
	ReasonInvalidStructure  Reason = "INVALID_STRUCTURE"  // 配置结构非法（缺字段、范围越界等）
	ReasonUnknownFlag       Reason = "UNKNOWN_FLAG"       // 求值的开关不存在
	ReasonInvalidAttribute  Reason = "INVALID_ATTRIBUTE"  // 灰度求值所需属性缺失或非法
	ReasonForbiddenOverride Reason = "FORBIDDEN_OVERRIDE" // 越权覆盖（低优先级来源试图覆盖被锁定的键）
	ReasonVersionNotFound   Reason = "VERSION_NOT_FOUND"  // 回滚目标版本不在历史记录中
)

// Error 是带可区分原因的引擎错误。
type Error struct {
	Reason  Reason
	Message string
}

func (e *Error) Error() string { return string(e.Reason) + ": " + e.Message }

func newError(r Reason, msg string) *Error { return &Error{Reason: r, Message: msg} }
