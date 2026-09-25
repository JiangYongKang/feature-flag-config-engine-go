package engine

import (
	"hash/fnv"
	"strconv"
)

// bucket computes a stable bucket in [0,100) for (flagKey, ruleName,
// bucketValue). FNV-1a 32-bit is used: it is a pure function of its input,
// so the same request attribute produces the same bucket across repeated
// evaluations and across process restarts (no randomness, no in-memory
// assignment table). Including flag and rule names prevents correlated
// rollouts of different rules from always landing on the same users.
func bucket(flagKey, ruleName, bucketValue string) (uint32, int) {
	h := fnv.New32a()
	_, _ = h.Write([]byte(flagKey))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(ruleName))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(bucketValue))
	sum := h.Sum32()
	return sum, int(sum % 100)
}

// attrString renders an attribute value canonically for matching and
// bucketing. Only the JSON-compatible scalar types are accepted; objects
// and arrays are rejected upstream as invalid attributes.
func attrString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case bool:
		return strconv.FormatBool(t), true
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64), true
	case int:
		return strconv.Itoa(t), true
	case int64:
		return strconv.FormatInt(t, 10), true
	case nil:
		return "", true
	default:
		return "", false
	}
}
