package engine

import (
	"fmt"
	"hash/fnv"
	"strings"
)

// bucketOf 对 flagKey 与属性值做 FNV-1a 哈希，得到 0..9999 的稳定分桶。
// 只依赖输入字符串，进程重启后结果不变。
func bucketOf(flagKey, attrValue string) int {
	h := fnv.New64a()
	h.Write([]byte(flagKey))
	h.Write([]byte{0x1f})
	h.Write([]byte(attrValue))
	return int(h.Sum64() % 10000)
}

// Decision 是一次求值的可解释结论。
type Decision struct {
	FlagKey string
	Value   Value
	Reason  string // 判定依据："default" / "rollout-hit" / "rollout-miss"
	Bucket  int    // 灰度分桶结果 0..9999（未灰度为 -1）
	Version uint64 // 求值所用快照版本
}

// Eval 在快照上对 flagKey 按属性求值。同一输入必须稳定命中同一结果。
func Eval(s *Snapshot, flagKey string, attrs map[string]string) (*Decision, error) {
	if s == nil {
		return nil, newError(ReasonInvalidStructure, "nil snapshot")
	}
	def, ok := s.Flags[flagKey]
	if !ok {
		return nil, newError(ReasonUnknownFlag,
			fmt.Sprintf("flag %q not defined in snapshot v%d", flagKey, s.Version))
	}
	if def.Rollout == nil {
		return &Decision{
			FlagKey: flagKey, Value: def.Default,
			Reason: "default", Bucket: -1, Version: s.Version,
		}, nil
	}
	attrVal, ok := attrs[def.Rollout.Attribute]
	if !ok || strings.TrimSpace(attrVal) == "" {
		return nil, newError(ReasonInvalidAttribute,
			fmt.Sprintf("flag %q rollout requires non-empty attribute %q",
				flagKey, def.Rollout.Attribute))
	}
	bucket := bucketOf(flagKey, attrVal)
	threshold := int(def.Rollout.Percentage * 100) // 0..10000
	if bucket < threshold {
		return &Decision{
			FlagKey: flagKey,
			Value:   Value{Type: TypeBool, Bool: true},
			Reason:  "rollout-hit", Bucket: bucket, Version: s.Version,
		}, nil
	}
	return &Decision{
		FlagKey: flagKey,
		Value:   Value{Type: TypeBool, Bool: false},
		Reason:  "rollout-miss", Bucket: bucket, Version: s.Version,
	}, nil
}
