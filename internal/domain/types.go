// Package domain defines the core configuration model and explainable errors
// for the feature flag / dynamic config engine.
package domain

// Reason is a machine-readable, distinguishable error reason.
// Invalid configuration must never silently fall back to defaults; callers
// can switch on these values to locate the precise cause.
type Reason string

const (
	// ReasonSourceUnreadable: a configured source could not be read at all
	// (file missing, IO error, ...).
	ReasonSourceUnreadable Reason = "source_unreadable"
	// ReasonInvalidJSON: source payload is not valid JSON.
	ReasonInvalidJSON Reason = "invalid_json"
	// ReasonTypeMismatch: a node exists but its JSON type is not the
	// declared/expected one.
	ReasonTypeMismatch Reason = "type_mismatch"
	// ReasonIllegalStructure: structurally invalid config
	// (bad percentage, operator, rule nesting, ...).
	ReasonIllegalStructure Reason = "illegal_structure"
	// ReasonDuplicateKey: same flag/property key appears twice inside one
	// source document.
	ReasonDuplicateKey Reason = "duplicate_key"
	// ReasonUnknownFlag: evaluated flag key does not exist in any source.
	ReasonUnknownFlag Reason = "unknown_flag"
	// ReasonUnknownProperty: requested property key does not exist in any source.
	ReasonUnknownProperty Reason = "unknown_property"
	// ReasonInvalidAttribute: request attribute key/type is not allowed.
	ReasonInvalidAttribute Reason = "invalid_attribute"
	// ReasonOverrideDenied: an evaluation-time override targets a flag/key
	// that is not in the flag's allow-override list.
	ReasonOverrideDenied Reason = "override_denied"
	// ReasonRuleIllegal: a rule cannot be evaluated (missing operand, ...).
	ReasonRuleIllegal Reason = "rule_illegal"
	// ReasonNoWinner: no rule matched and no default is declared
	// (configuration problem, must not be guessed).
	ReasonNoWinner Reason = "no_winner"
)

// Error is an explainable failure carrying a stable reason code plus a
// human-readable message and, when applicable, the config path that caused it.
type Error struct {
	Reason Reason
	Path   string // e.g. "flags.checkout_new.rules[1].percentage"
	Source string // source name, when known
	Msg    string
}

func (e *Error) Error() string {
	prefix := string(e.Reason)
	if e.Source != "" {
		prefix += " source=" + e.Source
	}
	if e.Path != "" {
		prefix += " path=" + e.Path
	}
	if e.Msg != "" {
		prefix += ": " + e.Msg
	}
	return prefix
}

// Is lets errors.Is match by Reason.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok {
		return false
	}
	return t.Reason == e.Reason
}

// Config is the merged, immutable configuration document.
type Config struct {
	Flags      map[string]Flag
	Properties map[string]Property
	// Provenance records, per key, which source won the merge.
	FlagProvenance     map[string]string
	PropertyProvenance map[string]string
	// SourceOrder is the priority order actually used: index 0 = highest.
	SourceOrder []string
}

// ValueType enumerates the supported dynamic-config value types.
type ValueType string

const (
	TypeBool   ValueType = "bool"
	TypeNumber ValueType = "number"
	TypeString ValueType = "string"
)

// Property is a typed dynamic configuration value.
type Property struct {
	Type ValueType
	Bool bool
	Num  float64
	Str  string
}

// Operator enumerates the deterministic rule matchers.
type Operator string

const (
	OpEq         Operator = "eq"
	OpNe         Operator = "ne"
	OpIn         Operator = "in"
	OpNotIn      Operator = "not_in"
	OpGt         Operator = "gt"
	OpLt         Operator = "lt"
	OpGe         Operator = "ge"
	OpLe         Operator = "le"
	OpStartsWith Operator = "starts_with"
	OpEndsWith   Operator = "ends_with"
)

// Rule is one branch of a flag's ordered rule list. First match wins.
type Rule struct {
	Name       string
	Attribute  string
	Operator   Operator
	Values     []string // compared against the string form of attributes
	Percentage int      // 0..100; 100 (or zero value semantics) means unconditional serve
	BucketBy   string   // attribute used for stable percentage bucketing
	Variation  string
}

// Flag is a single feature flag definition.
type Flag struct {
	Key              string
	DefaultVariation string
	Rules            []Rule
	// AllowOverride lists flag/property keys that callers may override at
	// evaluation time. Empty means: no overrides allowed at all.
	AllowOverride []string
}

// Attributes is the request-scoped evaluation input.
type Attributes map[string]any

// TraceStep explains one evaluation decision for audit/log output.
type TraceStep struct {
	Step       string `json:"step"`
	Detail     string `json:"detail"`
	Source     string `json:"source,omitempty"`
	Matched    bool   `json:"matched,omitempty"`
	BucketHash uint32 `json:"bucket_hash,omitempty"`
}

// Decision is the explainable result of a flag evaluation.
type Decision struct {
	Flag      string      `json:"flag"`
	Variation string      `json:"variation"`
	Value     bool        `json:"value"` // bool interpretation of variation
	Source    string      `json:"source"`
	Reason    string      `json:"reason"` // "default" | "rule:<name>"
	Trace     []TraceStep `json:"trace"`
}
