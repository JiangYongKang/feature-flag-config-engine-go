package parse

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/highcumontoa/feature-flag-config-engine-go/internal/domain"
)

// rootWire is the raw on-disk shape of one source document.
type rootWire struct {
	Flags      map[string]json.RawMessage `json:"flags"`
	Properties map[string]json.RawMessage `json:"properties"`
}

// strictDecode parses JSON with unknown-field rejection and duplicate-key
// detection. Type mismatches and structural JSON problems are normalized to
// distinguishable domain errors rather than being silently tolerated.
func strictDecode(sourceName string, raw []byte) (*rootWire, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, domain.Err(domain.ReasonInvalidJSON, "empty document").From(sourceName)
	}

	// First pass: plain decode to validate syntax and top-level shape.
	var root rootWire
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&root); err != nil {
		return nil, classifyJSONError(sourceName, err)
	}
	// Reject trailing tokens (multiple JSON values concatenated).
	if dec.More() {
		return nil, domain.Err(domain.ReasonInvalidJSON, "trailing data after JSON document").From(sourceName)
	}

	// Second pass: walk raw tokens to detect duplicate keys, which
	// encoding/json silently keeps the-last-value for.
	if err := detectDuplicateKeys(sourceName, "$", raw); err != nil {
		return nil, err
	}
	return &root, nil
}

// classifyJSONError maps encoding/json errors onto our reason codes.
func classifyJSONError(sourceName string, err error) error {
	switch e := err.(type) {
	case *json.UnmarshalTypeError:
		path := e.Field
		if path == "" {
			path = "$"
		}
		return domain.Err(domain.ReasonTypeMismatch,
			"expected %s at %s", e.Type.String(), path).At(path).From(sourceName)
	case *json.SyntaxError:
		return domain.Err(domain.ReasonInvalidJSON,
			"syntax error at offset %d: %v", e.Offset, e).From(sourceName)
	default:
		// DisallowUnknownFields surfaces here, as do other decode errors.
		msg := err.Error()
		if bytes.Contains([]byte(msg), []byte("unknown field")) {
			return domain.Err(domain.ReasonIllegalStructure, "%s", msg).From(sourceName)
		}
		return domain.Err(domain.ReasonInvalidJSON, "%s", msg).From(sourceName)
	}
}

// detectDuplicateKeys tokenizes raw and reports the first duplicated member
// name within the same JSON object.
func detectDuplicateKeys(sourceName, basePath string, raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var walk func(path string) error
	walk = func(path string) error {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		d, ok := tok.(json.Delim)
		if !ok {
			return nil // scalar value
		}
		switch d {
		case '{':
			keys := map[string]bool{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return err
				}
				key := kt.(string)
				childPath := fmt.Sprintf("%s.%s", path, key)
				if keys[key] {
					return domain.Err(domain.ReasonDuplicateKey,
						"key %q appears more than once in object %s", key, path).
						At(childPath).From(sourceName)
				}
				keys[key] = true
				if err := walk(childPath); err != nil {
					return err
				}
			}
			_, err := dec.Token() // closing '}'
			return err
		case '[':
			i := 0
			for dec.More() {
				if err := walk(fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
				i++
			}
			_, err := dec.Token() // closing ']'
			return err
		}
		return nil
	}
	return walk(basePath)
}
