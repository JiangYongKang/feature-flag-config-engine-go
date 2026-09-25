// Package parse decodes and strictly validates one raw JSON document.
package parse

import "github.com/highcumontoa/feature-flag-config-engine-go/internal/domain"

// Document is one source's parsed, validated contribution.
type Document struct {
	SourceName string
	Flags      map[string]domain.Flag
	Properties map[string]domain.Property
}

// Parse parses and validates raw JSON into a Document.
// It never applies defaults on error: type mismatches and illegal
// structures are rejected with a distinguishable domain.Error carrying a
// precise config path.
func Parse(sourceName string, raw []byte) (*Document, error) {
	root, err := strictDecode(sourceName, raw)
	if err != nil {
		return nil, err
	}
	doc := &Document{
		SourceName: sourceName,
		Flags:      map[string]domain.Flag{},
		Properties: map[string]domain.Property{},
	}
	for key, rawFlag := range root.Flags {
		f, err := parseFlag(sourceName, key, rawFlag)
		if err != nil {
			return nil, err
		}
		doc.Flags[key] = f
	}
	for key, rawProp := range root.Properties {
		p, err := parseProperty(sourceName, key, rawProp)
		if err != nil {
			return nil, err
		}
		doc.Properties[key] = p
	}
	return doc, nil
}
