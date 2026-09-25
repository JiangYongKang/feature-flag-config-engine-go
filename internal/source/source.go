// Package source defines configuration sources: where raw bytes come from.
package source

// Source is a single named configuration layer.
type Source interface {
	// Name identifies the layer in provenance and error messages.
	Name() string
	// Read returns the raw document bytes. A missing/unreadable source
	// must return an error; callers never silently skip it.
	Read() ([]byte, error)
}
