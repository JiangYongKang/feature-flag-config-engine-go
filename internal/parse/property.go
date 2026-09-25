package parse

import (
	"encoding/json"

	"github.com/highcumontoa/feature-flag-config-engine-go/internal/domain"
)

type propertyWire struct {
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value"`
}

// parseProperty strictly decodes one {"type":..., "value":...} entry.
func parseProperty(sourceName, key string, raw json.RawMessage) (domain.Property, error) {
	path := "properties." + key
	var w propertyWire
	if err := unmarshalStrict(sourceName, path, raw, &w); err != nil {
		return domain.Property{}, err
	}
	var p domain.Property
	switch domain.ValueType(w.Type) {
	case domain.TypeBool:
		p.Type = domain.TypeBool
		if err := json.Unmarshal(w.Value, &p.Bool); err != nil {
			return domain.Property{}, mismatch(sourceName, path+".value", "bool", w.Value)
		}
	case domain.TypeNumber:
		p.Type = domain.TypeNumber
		if err := json.Unmarshal(w.Value, &p.Num); err != nil {
			return domain.Property{}, mismatch(sourceName, path+".value", "number", w.Value)
		}
	case domain.TypeString:
		p.Type = domain.TypeString
		if err := json.Unmarshal(w.Value, &p.Str); err != nil {
			return domain.Property{}, mismatch(sourceName, path+".value", "string", w.Value)
		}
	default:
		return domain.Property{}, domain.Err(domain.ReasonIllegalStructure,
			"property %q has unsupported type %q (want bool|number|string)", key, w.Type).
			At(path + ".type").From(sourceName)
	}
	return p, nil
}

// mismatch builds a type_mismatch error showing the offending JSON token.
func mismatch(sourceName, path, expected string, raw json.RawMessage) error {
	got := "null"
	if len(raw) > 0 {
		got = string(raw)
		if len(got) > 40 {
			got = got[:40] + "..."
		}
	}
	return domain.Err(domain.ReasonTypeMismatch,
		"expected %s, got %s", expected, got).At(path).From(sourceName)
}
