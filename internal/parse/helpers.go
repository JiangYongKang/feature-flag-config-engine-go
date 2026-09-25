package parse

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/highcumontoa/feature-flag-config-engine-go/internal/domain"
)

// unmarshalStrict decodes raw into v with unknown-field rejection and
// normalizes JSON errors to domain errors anchored at path.
func unmarshalStrict(sourceName, path string, raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if ute, ok := err.(*json.UnmarshalTypeError); ok {
			return domain.Err(domain.ReasonTypeMismatch,
				"expected %s", ute.Type.String()).
				At(joinPath(path, ute.Field)).From(sourceName)
		}
		emsg := err.Error()
		if bytes.Contains([]byte(emsg), []byte("unknown field")) {
			return domain.Err(domain.ReasonIllegalStructure, "%s", emsg).
				At(path).From(sourceName)
		}
		return domain.Err(domain.ReasonInvalidJSON, "%s", emsg).
			At(path).From(sourceName)
	}
	if dec.More() {
		return domain.Err(domain.ReasonInvalidJSON, "trailing data in value").
			At(path).From(sourceName)
	}
	return nil
}

func joinPath(base, field string) string {
	if field == "" {
		return base
	}
	return base + "." + field
}

// itoa is a tiny local wrapper to keep error paths readable.
func itoa(i int) string { return strconv.Itoa(i) }
