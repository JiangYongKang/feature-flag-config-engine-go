package engine

import (
	"errors"
	"sync"
	"testing"

	"github.com/highcumontoa/feature-flag-config-engine-go/internal/domain"
	"github.com/highcumontoa/feature-flag-config-engine-go/internal/source"
)

// mustEngine builds an engine from raw source layers (earlier = higher
// priority) or fails the test.
func mustEngine(t *testing.T, layers ...[2]string) *Engine {
	t.Helper()
	srcs := make([]source.Source, 0, len(layers))
	for _, l := range layers {
		srcs = append(srcs, source.NewStatic(l[0], []byte(l[1])))
	}
	eng, err := New(srcs)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	return eng
}

func TestLayeredMergePriorityAndProvenance(t *testing.T) {
	base := `{
	  "flags": {"f": {"default_variation": "off",
	    "rules": [{"name":"all","attribute":"g","operator":"eq","values":["x"],"variation":"on"}]}},
	  "properties": {"p": {"type":"string","value":"base"}}
	}`
	over := `{
	  "flags": {"f": {"default_variation": "on", "rules": []}},
	  "properties": {"p": {"type":"string","value":"over"}}
	}`
	eng := mustEngine(t, [2]string{"override", over}, [2]string{"base", base})

	d, err := eng.EvaluateFlag("f", domain.Attributes{"g": "x"}, nil)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	// Whole-flag override wins: base's rule must not even be considered.
	if d.Source != "override" || d.Reason != "default" || d.Variation != "on" {
		t.Fatalf("want override-layer default on, got source=%s reason=%s variation=%s trace=%+v",
			d.Source, d.Reason, d.Variation, d.Trace)
	}
	t.Logf("INPUT attrs={g:x} | VERDICT variation=%s reason=%s source=%s", d.Variation, d.Reason, d.Source)

	if v, src, err := eng.GetString("p"); err != nil || v != "over" || src != "override" {
		t.Fatalf("property want over/override, got %q/%s err=%v", v, src, err)
	}
}

func TestPercentageStableAcrossRepeatsAndEngines(t *testing.T) {
	doc := `{
	  "flags": {"rollout": {"default_variation": "off", "rules": [
	    {"name":"half","attribute":"region","operator":"eq","values":["cn"],
	     "percentage":50,"bucket_by":"user_id","variation":"on"}
	  ]}}
	}`
	attrs := domain.Attributes{"region": "cn", "user_id": "user-42"}

	eng1 := mustEngine(t, [2]string{"s", doc})
	first, err := eng1.EvaluateFlag("rollout", attrs, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Repeat evaluation in the same process: must be identical.
	for i := 0; i < 20; i++ {
		d, _ := eng1.EvaluateFlag("rollout", attrs, nil)
		if d.Variation != first.Variation || d.Value != first.Value {
			t.Fatalf("unstable repeat eval at i=%d", i)
		}
	}
	// A brand new engine simulates process restart: same input, same answer.
	eng2 := mustEngine(t, [2]string{"s", doc})
	again, _ := eng2.EvaluateFlag("rollout", attrs, nil)
	if again.Variation != first.Variation {
		t.Fatalf("result changed after restart: before=%s after=%s", first.Variation, again.Variation)
	}
	t.Logf("INPUT %v bucket trace=%+v | VERDICT=%s (stable across 20 repeats + restart)",
		attrs, first.Trace, first.Variation)
}

func TestUnknownAndInvalidAttributeAndOverride(t *testing.T) {
	doc := `{"flags":{"f":{"default_variation":"off",
	  "allow_override":["other_flag"],
	  "rules":[{"name":"r","attribute":"age","operator":"gt","values":["18"],"variation":"on"}]}}}`
	eng := mustEngine(t, [2]string{"s", doc})

	_, errMissing := eng.EvaluateFlag("missing", nil, nil)
	if !reasonIs(errMissing, domain.ReasonUnknownFlag) {
		t.Fatalf("want unknown_flag, got %v", errMissing)
	}
	t.Logf("INPUT flag=missing | REJECT reason matches unknown_flag: %v", errMissing)

	// Non-numeric attribute against gt -> invalid_attribute, no silent coercion.
	_, err := eng.EvaluateFlag("f", domain.Attributes{"age": []string{"x"}}, nil)
	if !reasonIs(err, domain.ReasonInvalidAttribute) {
		t.Fatalf("want invalid_attribute for bad type, got %v", err)
	}
	t.Logf("INPUT attrs={age:[x]} | REJECT invalid_attribute: %v", err)

	// Override is not authorized for this flag -> override_denied.
	_, err = eng.EvaluateFlag("f", nil, map[string]any{"f": true})
	if !reasonIs(err, domain.ReasonOverrideDenied) {
		t.Fatalf("want override_denied, got %v", err)
	}
	t.Logf("INPUT override={f:true} allow_override=[other_flag] | REJECT override_denied: %v", err)

	// Override for a different key while evaluating f is also denied.
	_, err = eng.EvaluateFlag("f", nil, map[string]any{"other_flag": true})
	if !reasonIs(err, domain.ReasonOverrideDenied) {
		t.Fatalf("want override_denied for foreign key, got %v", err)
	}
}

func TestAuthorizedOverrideApplies(t *testing.T) {
	doc := `{"flags":{"f":{"default_variation":"off","allow_override":["f"],"rules":[]}}}`
	eng := mustEngine(t, [2]string{"s", doc})
	d, err := eng.EvaluateFlag("f", nil, map[string]any{"f": true})
	if err != nil || !d.Value || d.Reason != "override" {
		t.Fatalf("authorized override failed: %+v err=%v", d, err)
	}
	t.Logf("INPUT override={f:true} allow_override=[f] | VERDICT value=true reason=override")
}

func TestConcurrentReadersSeeConsistentSnapshots(t *testing.T) {
	v1 := `{"properties":{"p":{"type":"string","value":"v1"}}}`
	v2 := `{"properties":{"p":{"type":"string","value":"v2"}}}`
	src := &swapSource{name: "swap", good: []byte(v1), cur: []byte(v1)}
	eng, err := New([]source.Source{src})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	// Readers must only ever observe complete values v1 or v2.
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					v, _, err := eng.GetString("p")
					if err != nil || (v != "v1" && v != "v2") {
						t.Errorf("torn/inconsistent read: %q err=%v", v, err)
						return
					}
				}
			}
		}()
	}
	// Writer alternates valid snapshots.
	for i := 0; i < 50; i++ {
		src.mu.Lock()
		if i%2 == 0 {
			src.cur = []byte(v2)
		} else {
			src.cur = src.good
		}
		src.mu.Unlock()
		if err := eng.Reload(); err != nil {
			t.Fatalf("reload: %v", err)
		}
	}
	close(stop)
	wg.Wait()
	t.Logf("concurrent 16 readers x 50 atomic swaps: only complete snapshots observed, final=%s",
		eng.SnapshotVersion())
}

func TestFailedReloadKeepsOldSnapshot(t *testing.T) {
	good := `{"properties":{"p":{"type":"string","value":"good"}}}`
	bad := `{"properties":{"p":{"type":"number","value":"not-a-number"}}}`
	src := &swapSource{name: "swap", good: []byte(good), cur: []byte(good)}
	eng, err := New([]source.Source{src})
	if err != nil {
		t.Fatal(err)
	}
	before, _, _ := eng.GetString("p")

	src.mu.Lock()
	src.cur = []byte(bad)
	src.mu.Unlock()
	err = eng.Reload()
	if !reasonIs(err, domain.ReasonTypeMismatch) {
		t.Fatalf("want type_mismatch on reload, got %v", err)
	}
	after, srcName, gerr := eng.GetString("p")
	if gerr != nil || after != before {
		t.Fatalf("old snapshot must stay in effect: before=%s after=%s err=%v", before, after, gerr)
	}
	t.Logf("INPUT bad reload payload | REJECT %v | old value %q from %s still in force",
		err, after, srcName)
}

func reasonIs(err error, r domain.Reason) bool {
	var de *domain.Error
	return errors.As(err, &de) && de.Reason == r
}

type swapSource struct {
	name string
	good []byte
	bad  []byte
	cur  []byte
	mu   sync.Mutex
}

func (s *swapSource) Name() string { return s.name }
func (s *swapSource) Read() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]byte, len(s.cur))
	copy(out, s.cur)
	return out, nil
}

func TestMissingSourceRejectedNotSilentlySkipped(t *testing.T) {

	src := source.NewFile("ghost", "testdata/does-not-exist.json")
	_, err := New([]source.Source{src})
	if !reasonIs(err, domain.ReasonSourceUnreadable) {
		t.Fatalf("want source_unreadable, got %v", err)
	}
	t.Logf("INPUT missing file | REJECT source_unreadable: %v", err)
}

func TestPropertyTypeMismatchRejected(t *testing.T) {
	doc := `{"properties":{"n":{"type":"number","value":42}}}`
	eng := mustEngine(t, [2]string{"s", doc})

	_, _, errStr := eng.GetString("n")
	if !reasonIs(errStr, domain.ReasonTypeMismatch) {
		t.Fatalf("want type_mismatch requesting string of number, got %v", errStr)
	}
	t.Logf("INPUT GetString(number property) | REJECT type_mismatch: %v", errStr)
	_, _, errAbsent := eng.GetNumber("absent")
	if !reasonIs(errAbsent, domain.ReasonUnknownProperty) {
		t.Fatalf("want unknown_property, got %v", errAbsent)
	}
}
