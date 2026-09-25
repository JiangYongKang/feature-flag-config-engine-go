package engine

import (
	"strconv"
	"strings"

	"github.com/highcumontoa/feature-flag-config-engine-go/internal/domain"
)

// matchRule reports whether attrs satisfy r's condition. The second return
// value carries an explanation fragment used in the evaluation trace.
func matchRule(r domain.Rule, attrs domain.Attributes) (bool, string, error) {
	raw, present := attrs[r.Attribute]
	if !present {
		return false, "attribute %q not present", nil
	}
	s, ok := attrString(raw)
	if !ok {
		return false, "", domain.Err(domain.ReasonInvalidAttribute,
			"attribute %q has unsupported type %T", r.Attribute, raw)
	}
	switch r.Operator {
	case domain.OpEq:
		return s == r.Values[0], cmpFrag("eq", s, r.Values[0]), nil
	case domain.OpNe:
		return s != r.Values[0], cmpFrag("ne", s, r.Values[0]), nil
	case domain.OpIn:
		return contains(r.Values, s), cmpFrag("in", s, strings.Join(r.Values, ",")), nil
	case domain.OpNotIn:
		return !contains(r.Values, s), cmpFrag("not_in", s, strings.Join(r.Values, ",")), nil
	case domain.OpStartsWith:
		return strings.HasPrefix(s, r.Values[0]), cmpFrag("starts_with", s, r.Values[0]), nil
	case domain.OpEndsWith:
		return strings.HasSuffix(s, r.Values[0]), cmpFrag("ends_with", s, r.Values[0]), nil
	case domain.OpGt, domain.OpLt, domain.OpGe, domain.OpLe:
		return compareNumeric(r, s)
	default:
		return false, "", domain.Err(domain.ReasonRuleIllegal,
			"operator %q not implemented", r.Operator)
	}
}

func compareNumeric(r domain.Rule, s string) (bool, string, error) {
	got, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return false, "", domain.Err(domain.ReasonInvalidAttribute,
			"attribute %q value %q is not numeric for operator %s",
			r.Attribute, s, r.Operator)
	}
	// Ordering operators are validated to carry exactly one value.
	if len(r.Values) != 1 {
		return false, "", domain.Err(domain.ReasonRuleIllegal,
			"operator %s requires exactly one value", r.Operator)
	}
	want, err := strconv.ParseFloat(r.Values[0], 64)
	if err != nil {
		return false, "", domain.Err(domain.ReasonIllegalStructure,
			"rule %q value %q is not numeric", r.Name, r.Values[0])
	}
	var hit bool
	switch r.Operator {
	case domain.OpGt:
		hit = got > want
	case domain.OpLt:
		hit = got < want
	case domain.OpGe:
		hit = got >= want
	case domain.OpLe:
		hit = got <= want
	}
	return hit, cmpFrag(string(r.Operator), s, r.Values[0]), nil
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func cmpFrag(op, got, want string) string {
	return op + " [actual=" + got + " expected=" + want + "]"
}
