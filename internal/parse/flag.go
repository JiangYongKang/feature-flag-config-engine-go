package parse

import (
	"encoding/json"

	"github.com/highcumontoa/feature-flag-config-engine-go/internal/domain"
)

type flagWire struct {
	DefaultVariation string     `json:"default_variation"`
	Rules            []ruleWire `json:"rules"`
	AllowOverride    []string   `json:"allow_override"`
}

type ruleWire struct {
	Name       string   `json:"name"`
	Attribute  string   `json:"attribute"`
	Operator   string   `json:"operator"`
	Values     []string `json:"values"`
	Percentage *int     `json:"percentage"`
	BucketBy   string   `json:"bucket_by"`
	Variation  string   `json:"variation"`
}

var validOperators = map[domain.Operator]bool{
	domain.OpEq: true, domain.OpNe: true, domain.OpIn: true,
	domain.OpNotIn: true, domain.OpGt: true, domain.OpLt: true,
	domain.OpGe: true, domain.OpLe: true,
	domain.OpStartsWith: true, domain.OpEndsWith: true,
}

func parseFlag(sourceName, key string, raw json.RawMessage) (domain.Flag, error) {
	path := "flags." + key
	var w flagWire
	if err := unmarshalStrict(sourceName, path, raw, &w); err != nil {
		return domain.Flag{}, err
	}
	f := domain.Flag{
		Key:              key,
		DefaultVariation: w.DefaultVariation,
		AllowOverride:    w.AllowOverride,
	}
	if f.DefaultVariation == "" {
		return domain.Flag{}, domain.Err(domain.ReasonIllegalStructure,
			"flag %q must declare non-empty default_variation", key).
			At(path + ".default_variation").From(sourceName)
	}
	for i, rw := range w.Rules {
		r, err := parseRule(sourceName, path, i, rw)
		if err != nil {
			return domain.Flag{}, err
		}
		f.Rules = append(f.Rules, r)
	}
	return f, nil
}

func parseRule(sourceName, flagPath string, i int, w ruleWire) (domain.Rule, error) {
	path := flagPath + ".rules[" + itoa(i) + "]"
	if w.Name == "" {
		return domain.Rule{}, domain.Err(domain.ReasonIllegalStructure,
			"rule at index %d must have a name", i).At(path + ".name").From(sourceName)
	}
	op := domain.Operator(w.Operator)
	if !validOperators[op] {
		return domain.Rule{}, domain.Err(domain.ReasonIllegalStructure,
			"rule %q has unsupported operator %q", w.Name, w.Operator).
			At(path + ".operator").From(sourceName)
	}
	if w.Attribute == "" {
		return domain.Rule{}, domain.Err(domain.ReasonIllegalStructure,
			"rule %q must name an attribute", w.Name).
			At(path + ".attribute").From(sourceName)
	}
	needsValues := op != domain.OpGt && op != domain.OpLt && op != domain.OpGe && op != domain.OpLe
	if needsValues && len(w.Values) == 0 {
		return domain.Rule{}, domain.Err(domain.ReasonIllegalStructure,
			"rule %q operator %s requires at least one value", w.Name, op).
			At(path + ".values").From(sourceName)
	}
	pct := 100
	if w.Percentage != nil {
		pct = *w.Percentage
	}
	if pct < 0 || pct > 100 {
		return domain.Rule{}, domain.Err(domain.ReasonIllegalStructure,
			"rule %q percentage must be within 0..100, got %d", w.Name, pct).
			At(path + ".percentage").From(sourceName)
	}
	if pct < 100 && w.BucketBy == "" {
		return domain.Rule{}, domain.Err(domain.ReasonIllegalStructure,
			"rule %q with percentage < 100 must declare bucket_by", w.Name).
			At(path + ".bucket_by").From(sourceName)
	}
	if w.Variation == "" {
		return domain.Rule{}, domain.Err(domain.ReasonIllegalStructure,
			"rule %q must declare variation", w.Name).
			At(path + ".variation").From(sourceName)
	}
	return domain.Rule{
		Name:       w.Name,
		Attribute:  w.Attribute,
		Operator:   op,
		Values:     w.Values,
		Percentage: pct,
		BucketBy:   w.BucketBy,
		Variation:  w.Variation,
	}, nil
}
