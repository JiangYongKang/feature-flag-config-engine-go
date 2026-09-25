// Package engine provides atomic snapshots, hot reload and stable
// percentage-based gradual rollout evaluation.
package engine

import (
	"sync/atomic"

	"github.com/highcumontoa/feature-flag-config-engine-go/internal/domain"
	"github.com/highcumontoa/feature-flag-config-engine-go/internal/source"
)

// Engine holds an immutable snapshot and swaps it atomically on reload.
// All methods are safe for concurrent use: readers take no locks.
type Engine struct {
	sources []source.Source
	snap    atomic.Pointer[snapshot]
	seq     atomic.Uint64
}

// New builds an engine and performs the initial load. If any source is
// missing or invalid, New fails: there is no silent empty-default state.
func New(sources []source.Source) (*Engine, error) {
	if len(sources) == 0 {
		return nil, domain.Err(domain.ReasonIllegalStructure,
			"at least one configuration source is required")
	}
	e := &Engine{sources: sources}
	first, err := e.load()
	if err != nil {
		return nil, err
	}
	first.version = e.seq.Add(1)
	e.snap.Store(first)
	return e, nil
}

// EvaluateFlag evaluates a flag for the given attributes and optional
// per-evaluation overrides.
func (e *Engine) EvaluateFlag(key string, attrs domain.Attributes, overrides map[string]any) (*domain.Decision, error) {
	return e.evaluate(e.current(), key, attrs, overrides)
}

// GetProperty returns a typed property and the name of the source that won.
func (e *Engine) GetProperty(key string) (domain.Property, string, error) {
	s := e.current()
	p, ok := s.cfg.Properties[key]
	if !ok {
		return domain.Property{}, "", domain.Err(domain.ReasonUnknownProperty,
			"no source defines property %q", key)
	}
	return p, s.cfg.PropertyProvenance[key], nil
}
