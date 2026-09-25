package engine

import (
	"log"

	"github.com/highcumontoa/feature-flag-config-engine-go/internal/domain"
)

// Typed getters return a type_mismatch error instead of coercing or
// silently returning a zero value. The fallback argument is used ONLY by
// the explicit Or* helpers when the key is genuinely absent — the caller
// opts into the fallback, the engine never invents one silently.

func (e *Engine) GetBool(key string) (bool, string, error) {
	p, src, err := e.GetProperty(key)
	if err != nil {
		return false, "", err
	}
	if p.Type != domain.TypeBool {
		return false, src, typeMismatch(key, p.Type, domain.TypeBool)
	}
	return p.Bool, src, nil
}

func (e *Engine) GetNumber(key string) (float64, string, error) {
	p, src, err := e.GetProperty(key)
	if err != nil {
		return 0, "", err
	}
	if p.Type != domain.TypeNumber {
		return 0, src, typeMismatch(key, p.Type, domain.TypeNumber)
	}
	return p.Num, src, nil
}

func (e *Engine) GetString(key string) (string, string, error) {
	p, src, err := e.GetProperty(key)
	if err != nil {
		return "", "", err
	}
	if p.Type != domain.TypeString {
		return "", src, typeMismatch(key, p.Type, domain.TypeString)
	}
	log.Printf("[config-get] key=%s -> %q source=%s", key, p.Str, src)
	return p.Str, src, nil
}

func typeMismatch(key string, got, want domain.ValueType) error {
	return domain.Err(domain.ReasonTypeMismatch,
		"property %q declared as %s, requested as %s", key, got, want).
		At("properties." + key)
}
