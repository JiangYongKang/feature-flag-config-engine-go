package parse

import (
	"errors"
	"strings"
	"testing"

	"github.com/highcumontoa/feature-flag-config-engine-go/internal/domain"
)

func TestParseRejectsWithDistinguishableReasons(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		reason domain.Reason
		path   string
	}{
		{
			name:   "syntax error",
			raw:    `{not json`,
			reason: domain.ReasonInvalidJSON,
		},
		{
			name:   "property type mismatch",
			raw:    `{"properties":{"p":{"type":"number","value":"123"}}}`,
			reason: domain.ReasonTypeMismatch,
			path:   "properties.p.value",
		},
		{
			name:   "unknown property type",
			raw:    `{"properties":{"p":{"type":"array","value":1}}}`,
			reason: domain.ReasonIllegalStructure,
			path:   "properties.p.type",
		},
		{
			name: "percentage out of range",
			raw: `{"flags":{"f":{"default_variation":"off","rules":[
			        {"name":"r","attribute":"a","operator":"eq","values":["x"],"percentage":101,"variation":"on"}]}}}`,
			reason: domain.ReasonIllegalStructure,
			path:   "flags.f.rules[0].percentage",
		},
		{
			name: "bad operator",
			raw: `{"flags":{"f":{"default_variation":"off","rules":[
			        {"name":"r","attribute":"a","operator":"regex","values":["x"],"variation":"on"}]}}}`,
			reason: domain.ReasonIllegalStructure,
			path:   "flags.f.rules[0].operator",
		},
		{
			name: "percentage without bucket_by",
			raw: `{"flags":{"f":{"default_variation":"off","rules":[
			        {"name":"r","attribute":"a","operator":"eq","values":["x"],"percentage":10,"variation":"on"}]}}}`,
			reason: domain.ReasonIllegalStructure,
			path:   "flags.f.rules[0].bucket_by",
		},
		{
			name:   "missing default variation",
			raw:    `{"flags":{"f":{"rules":[]}}}`,
			reason: domain.ReasonIllegalStructure,
			path:   "flags.f.default_variation",
		},
		{
			name:   "duplicate key in one object",
			raw:    `{"properties":{"p":{"type":"string","value":"a","type":"string"}}}`,
			reason: domain.ReasonDuplicateKey,
		},
		{
			name:   "unknown field rejected",
			raw:    `{"unexpected_top":1}`,
			reason: domain.ReasonIllegalStructure,
		},
		{
			name:   "flags section has wrong json type",
			raw:    `{"flags":[]}`,
			reason: domain.ReasonTypeMismatch,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse("test-src", []byte(tc.raw))
			var de *domain.Error
			if !errors.As(err, &de) {
				t.Fatalf("INPUT %s | want *domain.Error, got %v", tc.raw, err)
			}
			if de.Reason != tc.reason {
				t.Fatalf("INPUT %s | want reason=%s, got reason=%s (%v)",
					tc.raw, tc.reason, de.Reason, err)
			}
			if tc.path != "" && !strings.HasPrefix(de.Path, tc.path) && de.Path != tc.path {
				t.Fatalf("want path prefix %s, got %s", tc.path, de.Path)
			}
			t.Logf("INPUT %-60s | REJECT reason=%s source=%s path=%s",
				tc.raw, de.Reason, de.Source, de.Path)
		})
	}
}

func TestParseValidDocument(t *testing.T) {
	raw := []byte(`{
	  "flags": {"f": {"default_variation": "off", "rules": [
	    {"name":"r","attribute":"g","operator":"in","values":["a","b"],"variation":"on"}]}},
	  "properties": {"n": {"type":"number","value":1.5}}
	}`)
	doc, err := Parse("ok", raw)
	if err != nil {
		t.Fatalf("valid doc rejected: %v", err)
	}
	if doc.Flags["f"].Rules[0].Percentage != 100 {
		t.Fatalf("percentage should default to 100, got %d", doc.Flags["f"].Rules[0].Percentage)
	}
	if doc.Properties["n"].Num != 1.5 {
		t.Fatalf("number property not parsed: %+v", doc.Properties["n"])
	}
	t.Logf("VALID INPUT parsed: 1 flag, 1 property; default percentage=100")
}
