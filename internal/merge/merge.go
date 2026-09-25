// Package merge merges validated source documents by deterministic priority.
package merge

import (
	"github.com/highcumontoa/feature-flag-config-engine-go/internal/domain"
	"github.com/highcumontoa/feature-flag-config-engine-go/internal/parse"
)

// NamedDocument pairs a document with the priority position of its source.
type NamedDocument = parse.Document

// Merge combines documents in strict priority order: docs[0] has the
// highest priority. A flag/property key is taken whole from the first
// source that defines it (no partial deep-merge, which would make the
// winning layer ambiguous); provenance records the winning source so every
// decision is explainable.
func Merge(docs []*NamedDocument) (*domain.Config, error) {
	cfg := &domain.Config{
		Flags:              map[string]domain.Flag{},
		Properties:         map[string]domain.Property{},
		FlagProvenance:     map[string]string{},
		PropertyProvenance: map[string]string{},
	}
	for _, d := range docs {
		if d == nil {
			continue
		}
		cfg.SourceOrder = append(cfg.SourceOrder, d.SourceName)
		// Iterate flags deterministically (sorted) for reproducible builds
		// of provenance; map content is the same either way.
		for _, key := range sortedFlagKeys(d.Flags) {
			if _, exists := cfg.Flags[key]; exists {
				continue // higher-priority source already won
			}
			cfg.Flags[key] = d.Flags[key]
			cfg.FlagProvenance[key] = d.SourceName
		}
		for _, key := range sortedPropertyKeys(d.Properties) {
			if _, exists := cfg.Properties[key]; exists {
				continue
			}
			cfg.Properties[key] = d.Properties[key]
			cfg.PropertyProvenance[key] = d.SourceName
		}
	}
	return cfg, nil
}
