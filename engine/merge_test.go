package engine

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func boolDef(key string, v bool) FlagDef {
	return FlagDef{Key: key, Type: TypeBool, Default: Value{Type: TypeBool, Bool: v}}
}

func TestMergePriorityOverride(t *testing.T) {
	low := NewMapSource("defaults", 0, map[string]FlagDef{
		"feature-a": boolDef("feature-a", false),
		"feature-b": boolDef("feature-b", false),
	})
	high := NewMapSource("remote", 10, map[string]FlagDef{
		"feature-a": boolDef("feature-a", true),
	})
	snap, err := Merge(1, []Source{low, high})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	t.Logf("merge inputs: low{feature-a:false,feature-b:false} high{feature-a:true} -> feature-a=%v feature-b=%v",
		snap.Flags["feature-a"].Default.Bool, snap.Flags["feature-b"].Default.Bool)
	if !snap.Flags["feature-a"].Default.Bool {
		t.Fatal("high priority source should win for feature-a")
	}
	if snap.Flags["feature-b"].Default.Bool {
		t.Fatal("feature-b should keep low priority value")
	}
}

func TestMergeForbiddenOverride(t *testing.T) {
	locked := boolDef("core-flag", true)
	locked.LockOverride = true
	low := NewMapSource("locked-base", 0, map[string]FlagDef{
		"core-flag": locked,
	})
	high := NewMapSource("tenant", 10, map[string]FlagDef{
		"core-flag": boolDef("core-flag", false),
	})
	_, err := Merge(1, []Source{low, high})
	var ee *Error
	if !errors.As(err, &ee) || ee.Reason != ReasonForbiddenOverride {
		t.Fatalf("want ReasonForbiddenOverride, got %v", err)
	}
	t.Logf("input: locked core-flag overridden by higher source -> rejected reason=%s msg=%s", ee.Reason, ee.Message)
}

func TestMergeSourceMissing(t *testing.T) {
	bad := NewMapSource("broken", 0, nil)
	bad.SetErr(errors.New("connection refused"))
	_, err := Merge(1, []Source{bad})
	var ee *Error
	if !errors.As(err, &ee) || ee.Reason != ReasonSourceMissing {
		t.Fatalf("want ReasonSourceMissing, got %v", err)
	}
	t.Logf("input: unavailable source -> rejected reason=%s msg=%s", ee.Reason, ee.Message)
}

func TestMergeTypeMismatch(t *testing.T) {
	bad := NewMapSource("bad-types", 0, map[string]FlagDef{
		"flag-x": {Key: "flag-x", Type: TypeBool, Default: Value{Type: TypeString, Str: "oops"}},
	})
	_, err := Merge(1, []Source{bad})
	var ee *Error
	if !errors.As(err, &ee) || ee.Reason != ReasonTypeMismatch {
		t.Fatalf("want ReasonTypeMismatch, got %v", err)
	}
	t.Logf("input: bool flag with string default -> rejected reason=%s msg=%s", ee.Reason, ee.Message)
}

func TestMergeInvalidStructure(t *testing.T) {
	bad := NewMapSource("bad-rollout", 0, map[string]FlagDef{
		"flag-y": {
			Key:     "flag-y",
			Type:    TypeBool,
			Default: Value{Type: TypeBool},
			Rollout: &RolloutRule{Attribute: "user_id", Percentage: 150},
		},
	})
	_, err := Merge(1, []Source{bad})
	var ee *Error
	if !errors.As(err, &ee) || ee.Reason != ReasonInvalidStructure {
		t.Fatalf("want ReasonInvalidStructure, got %v", err)
	}
	t.Logf("input: rollout percentage 150 -> rejected reason=%s msg=%s", ee.Reason, ee.Message)
}

func TestFileSourceLoadAndErrors(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	content := `{"flags":[
		{"key":"f1","type":"bool","default":true,"rollout":{"attribute":"user_id","percentage":25}},
		{"key":"f2","type":"number","default":3.14},
		{"key":"f3","type":"json","default":{"a":1}}
	]}`
	if err := os.WriteFile(good, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	src := &FileSource{SrcName: "file", SrcPriority: 1, Path: good}
	defs, err := src.Load()
	if err != nil {
		t.Fatalf("load good file: %v", err)
	}
	t.Logf("loaded file source: f1 rollout=%+v f2 default=%v f3 json=%s",
		defs["f1"].Rollout, defs["f2"].Default.Num, defs["f3"].Default.JSON)

	// 文件缺失 -> SOURCE_MISSING
	missing := &FileSource{SrcName: "missing", SrcPriority: 1, Path: filepath.Join(dir, "nope.json")}
	_, err = missing.Load()
	var ee *Error
	if !errors.As(err, &ee) || ee.Reason != ReasonSourceMissing {
		t.Fatalf("want ReasonSourceMissing, got %v", err)
	}
	t.Logf("input: missing file -> rejected reason=%s", ee.Reason)

	// 类型不匹配 -> TYPE_MISMATCH
	badType := filepath.Join(dir, "badtype.json")
	if err := os.WriteFile(badType, []byte(`{"flags":[{"key":"f","type":"bool","default":"yes"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = (&FileSource{SrcName: "badtype", SrcPriority: 1, Path: badType}).Load()
	if !errors.As(err, &ee) || ee.Reason != ReasonTypeMismatch {
		t.Fatalf("want ReasonTypeMismatch, got %v", err)
	}
	t.Logf("input: bool flag with string default in file -> rejected reason=%s msg=%s", ee.Reason, ee.Message)

	// 结构非法（未知类型） -> INVALID_STRUCTURE
	badStruct := filepath.Join(dir, "badstruct.json")
	if err := os.WriteFile(badStruct, []byte(`{"flags":[{"key":"f","type":"weird","default":1}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = (&FileSource{SrcName: "badstruct", SrcPriority: 1, Path: badStruct}).Load()
	if !errors.As(err, &ee) || ee.Reason != ReasonInvalidStructure {
		t.Fatalf("want ReasonInvalidStructure, got %v", err)
	}
	t.Logf("input: unknown flag type in file -> rejected reason=%s msg=%s", ee.Reason, ee.Message)
}
