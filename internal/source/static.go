package source

// Static is an in-memory source, useful for tests and embedded defaults.
type Static struct {
	name string
	raw  []byte
}

// NewStatic creates a named in-memory source.
func NewStatic(name string, raw []byte) *Static {
	return &Static{name: name, raw: raw}
}

func (s *Static) Name() string { return s.name }

func (s *Static) Read() ([]byte, error) {
	// Return a copy so callers cannot mutate the source's bytes.
	out := make([]byte, len(s.raw))
	copy(out, s.raw)
	return out, nil
}
