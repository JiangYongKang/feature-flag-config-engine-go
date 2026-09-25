package engine

// ValueType 配置值的声明类型。
type ValueType string

const (
	TypeBool    ValueType = "bool"
	TypeString  ValueType = "string"
	TypeNumber  ValueType = "number"
	TypeJSON    ValueType = "json"
)

// Value 是一个带类型的配置值。
type Value struct {
	Type ValueType
	Bool bool
	Str  string
	Num  float64
	JSON string // 原始 JSON 文本
}

// RolloutRule 按属性灰度的规则。
type RolloutRule struct {
	Attribute  string  // 用于哈希分桶的属性名，如 user_id
	Percentage float64 // 0-100，命中比例
}

// FlagDef 单个开关/配置项的定义。
type FlagDef struct {
	Key          string
	Type         ValueType
	Default      Value
	Rollout      *RolloutRule // 仅 bool 类型可配灰度
	LockOverride bool         // 锁定后低优先级来源不得覆盖
}

// Snapshot 是一次合并完成后的不可变完整配置快照。
type Snapshot struct {
	Version uint64
	Flags   map[string]FlagDef
}
