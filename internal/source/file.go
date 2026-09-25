package source

import (
	"os"

	"github.com/highcumontoa/feature-flag-config-engine-go/internal/domain"
)

// File reads a JSON document from disk on every Read, so a later Reload
// observes updated content without re-registration.
type File struct {
	name string
	path string
}

// NewFile creates a named file source bound to path.
func NewFile(name, path string) *File {
	return &File{name: name, path: path}
}

func (f *File) Name() string { return f.name }

func (f *File) Read() ([]byte, error) {
	raw, err := os.ReadFile(f.path)
	if err != nil {
		return nil, domain.Err(domain.ReasonSourceUnreadable,
			"cannot read %q: %v", f.path, err).From(f.name)
	}
	return raw, nil
}
