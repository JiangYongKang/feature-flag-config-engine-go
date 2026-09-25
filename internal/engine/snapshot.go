package engine

import (
	"fmt"

	"github.com/highcumontoa/feature-flag-config-engine-go/internal/domain"
	"github.com/highcumontoa/feature-flag-config-engine-go/internal/merge"
	"github.com/highcumontoa/feature-flag-config-engine-go/internal/parse"
)

// snapshot is an immutable view published atomically. Readers never see a
// partially merged state: a new snapshot is built completely before the
// single atomic pointer swap.
type snapshot struct {
	version uint64
	cfg     *domain.Config
}

// load reads, parses and merges every source. Any failure aborts the whole
// load and returns a distinguishable error — missing sources or invalid
// documents never silently downgrade to defaults.
func (e *Engine) load() (*snapshot, error) {
	docs := make([]*parse.Document, 0, len(e.sources))
	for _, src := range e.sources {
		raw, err := src.Read()
		if err != nil {
			return nil, err // already a domain.Error from the source
		}
		doc, err := parse.Parse(src.Name(), raw)
		if err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}
	cfg, err := merge.Merge(docs)
	if err != nil {
		return nil, err
	}
	return &snapshot{cfg: cfg}, nil
}

// current returns the active immutable snapshot.
func (e *Engine) current() *snapshot {
	return e.snap.Load()
}

// Reload re-reads all sources and atomically swaps in the new snapshot.
// On failure the previous snapshot remains in force and the error is
// returned (with reason + source + path for localization).
func (e *Engine) Reload() error {
	next, err := e.load()
	if err != nil {
		return err // old snapshot untouched
	}
	next.version = e.seq.Add(1)
	e.snap.Store(next)
	return nil
}

// SnapshotVersion returns a monotonic identifier of the active snapshot.
func (e *Engine) SnapshotVersion() string {
	s := e.current()
	return fmt.Sprintf("v%d", s.version)
}
