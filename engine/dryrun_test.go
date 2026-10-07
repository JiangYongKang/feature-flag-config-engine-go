package engine

import (
	"errors"
	"testing"
)

func rolloutBoolDef(key string, pct float64) FlagDef {
	return FlagDef{
		Key: key, Type: TypeBool,
		Default: Value{Type: TypeBool},
		Rollout: &RolloutRule{Attribute: "user_id", Percentage: pct},
	}
}

// TestDryRunReportCoversAddChangeRemoveAndShift 验证试算报告覆盖
// 新增、删除、变更以及灰度命中范围走向，且试算不影响线上生效配置。
func TestDryRunReportCoversAddChangeRemoveAndShift(t *testing.T) {
	live := NewMapSource("live", 0, map[string]FlagDef{
		"keep":     boolDef("keep", true),
		"removed":  boolDef("removed", true),
		"expanded": rolloutBoolDef("expanded", 20),
		"shrunk":   rolloutBoolDef("shrunk", 80),
		"retuned":  boolDef("retuned", false),
	})
	e, err := New(live)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	before := e.Snapshot()

	candidate := NewMapSource("candidate", 0, map[string]FlagDef{
		"keep":     boolDef("keep", true),       // 不变
		"added":    boolDef("added", true),      // 新增
		"expanded": rolloutBoolDef("expanded", 60), // 灰度扩大
		"shrunk":   rolloutBoolDef("shrunk", 30),   // 灰度缩小
		"retuned":  boolDef("retuned", true),    // 值变更
		// "removed" 缺席 -> 删除
	})
	report, err := e.DryRun(candidate)
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	t.Logf("dry-run base=v%d candidate=v%d changes=%d",
		report.BaseVersion, report.CandidateVersion, len(report.Changes))
	for _, c := range report.Changes {
		t.Logf("  change key=%s kind=%s rolloutShift=%q", c.Key, c.Kind, c.RolloutShift)
	}

	if report.BaseVersion != before.Version {
		t.Fatalf("base version = %d, want %d", report.BaseVersion, before.Version)
	}
	if report.CandidateVersion != before.Version+1 {
		t.Fatalf("candidate version = %d, want %d", report.CandidateVersion, before.Version+1)
	}

	wantKind := map[string]ChangeKind{
		"added": ChangeAdded, "removed": ChangeRemoved,
		"expanded": ChangeChanged, "shrunk": ChangeChanged, "retuned": ChangeChanged,
	}
	wantShift := map[string]string{
		"expanded": ShiftExpanded, "shrunk": ShiftShrunk, "retuned": "",
	}
	got := make(map[string]FlagChange)
	for _, c := range report.Changes {
		got[c.Key] = c
	}
	if len(got) != len(wantKind) {
		t.Fatalf("changes = %d, want %d (%v)", len(got), len(wantKind), got)
	}
	for key, kind := range wantKind {
		c, ok := got[key]
		if !ok {
			t.Fatalf("missing change for key %q", key)
		}
		if c.Kind != kind {
			t.Fatalf("key %q kind = %s, want %s", key, c.Kind, kind)
		}
		if c.RolloutShift != wantShift[key] {
			t.Fatalf("key %q rolloutShift = %q, want %q", key, c.RolloutShift, wantShift[key])
		}
	}
	if _, ok := got["keep"]; ok {
		t.Fatal("unchanged flag must not appear in report")
	}
	if len(report.Added()) != 1 || len(report.Removed()) != 1 || len(report.Changed()) != 3 {
		t.Fatalf("classified views wrong: added=%d removed=%d changed=%d",
			len(report.Added()), len(report.Removed()), len(report.Changed()))
	}

	// 试算不得影响线上：快照指针、版本号、求值结果全部不变
	after := e.Snapshot()
	if after != before {
		t.Fatal("dry-run must not swap the live snapshot")
	}
	d, err := e.Evaluate("retuned", nil)
	if err != nil || d.Value.Bool {
		t.Fatalf("live config must be untouched by dry-run, got %+v err=%v", d, err)
	}
	if len(e.History()) != 1 {
		t.Fatalf("dry-run must not record history, got %d entries", len(e.History()))
	}
	t.Logf("after dry-run: live snapshot still v%d, retuned=%v (untouched)", after.Version, d.Value.Bool)
}

// TestDryRunRejectsInvalidCandidate 验证候选内容非法时按与正式合并
// 一致的可区分原因拒绝，且线上配置不受影响。
func TestDryRunRejectsInvalidCandidate(t *testing.T) {
	locked := NewMapSource("locked", 0, map[string]FlagDef{
		"pinned": {Key: "pinned", Type: TypeBool,
			Default: Value{Type: TypeBool, Bool: true}, LockOverride: true},
	})
	e, err := New(locked)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	before := e.Snapshot()

	badType := NewMapSource("bad-type", 1, map[string]FlagDef{
		"x": {Key: "x", Type: TypeBool, Default: Value{Type: TypeString, Str: "no"}},
	})
	badStructure := NewMapSource("bad-structure", 1, map[string]FlagDef{
		"x": {Key: "x", Type: TypeBool, Default: Value{Type: TypeBool},
			Rollout: &RolloutRule{Attribute: "user_id", Percentage: 150}},
	})
	overrideLocked := NewMapSource("override", 1, map[string]FlagDef{
		"pinned": boolDef("pinned", false),
	})
	missing := NewMapSource("missing", 1, nil)
	missing.SetErr(errors.New("backend down"))

	cases := []struct {
		name string
		src  *MapSource
		want Reason
	}{
		{"type-mismatch", badType, ReasonTypeMismatch},
		{"invalid-structure", badStructure, ReasonInvalidStructure},
		{"forbidden-override", overrideLocked, ReasonForbiddenOverride},
		{"source-missing", missing, ReasonSourceMissing},
	}
	for _, tc := range cases {
		_, err := e.DryRun(locked, tc.src)
		var ee *Error
		if !errors.As(err, &ee) || ee.Reason != tc.want {
			t.Fatalf("%s: want reason %s, got %v", tc.name, tc.want, err)
		}
		t.Logf("%s: rejected reason=%s msg=%s", tc.name, ee.Reason, ee.Message)
		if e.Snapshot() != before {
			t.Fatalf("%s: rejected dry-run must not touch live snapshot", tc.name)
		}
	}
}
