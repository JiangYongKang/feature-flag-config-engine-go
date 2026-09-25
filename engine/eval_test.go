package engine

import (
	"errors"
	"testing"
)

func rolloutSnapshot() *Snapshot {
	return &Snapshot{
		Version: 1,
		Flags: map[string]FlagDef{
			"new-checkout": {
				Key:     "new-checkout",
				Type:    TypeBool,
				Default: Value{Type: TypeBool, Bool: false},
				Rollout: &RolloutRule{Attribute: "user_id", Percentage: 50},
			},
			"plain-flag": {
				Key:     "plain-flag",
				Type:    TypeString,
				Default: Value{Type: TypeString, Str: "hello"},
			},
		},
	}
}

func TestEvalRolloutDeterministic(t *testing.T) {
	snap := rolloutSnapshot()
	cases := []string{"user-1", "user-2", "user-100", "alice", "bob"}
	for _, uid := range cases {
		attrs := map[string]string{"user_id": uid}
		first, err := Eval(snap, "new-checkout", attrs)
		if err != nil {
			t.Fatalf("eval %q: %v", uid, err)
		}
		t.Logf("input={flag:new-checkout user_id:%s} -> value=%v reason=%s bucket=%d version=%d",
			uid, first.Value.Bool, first.Reason, first.Bucket, first.Version)
		// 重复求值 50 次必须一致
		for i := 0; i < 50; i++ {
			d, err := Eval(snap, "new-checkout", attrs)
			if err != nil {
				t.Fatalf("re-eval %q: %v", uid, err)
			}
			if d.Value.Bool != first.Value.Bool || d.Bucket != first.Bucket {
				t.Fatalf("input %q unstable: first=%v/%d got=%v/%d",
					uid, first.Value.Bool, first.Bucket, d.Value.Bool, d.Bucket)
			}
		}
		// 模拟进程重启：用相同定义重建快照再求值，结果必须一致
		restarted := rolloutSnapshot()
		d, err := Eval(restarted, "new-checkout", attrs)
		if err != nil {
			t.Fatalf("restart eval %q: %v", uid, err)
		}
		if d.Value.Bool != first.Value.Bool || d.Bucket != first.Bucket {
			t.Fatalf("input %q changed after restart: before=%v/%d after=%v/%d",
				uid, first.Value.Bool, first.Bucket, d.Value.Bool, d.Bucket)
		}
	}
}

func TestEvalRolloutDistribution(t *testing.T) {
	snap := rolloutSnapshot()
	hits := 0
	const total = 2000
	for i := 0; i < total; i++ {
		uid := "user-" + string(rune('a'+i%26)) + "-" + itoa(i)
		d, err := Eval(snap, "new-checkout", map[string]string{"user_id": uid})
		if err != nil {
			t.Fatalf("eval: %v", err)
		}
		if d.Value.Bool {
			hits++
		}
	}
	ratio := float64(hits) / float64(total)
	t.Logf("rollout 50%% over %d users: hits=%d ratio=%.3f", total, hits, ratio)
	if ratio < 0.40 || ratio > 0.60 {
		t.Fatalf("rollout ratio %.3f far from 0.5", ratio)
	}
}

func TestEvalMissingAttribute(t *testing.T) {
	snap := rolloutSnapshot()
	_, err := Eval(snap, "new-checkout", map[string]string{"other": "x"})
	var ee *Error
	if !errors.As(err, &ee) || ee.Reason != ReasonInvalidAttribute {
		t.Fatalf("want ReasonInvalidAttribute, got %v", err)
	}
	t.Logf("input={flag:new-checkout attrs:{other:x}} -> rejected reason=%s msg=%s", ee.Reason, ee.Message)
}

func TestEvalUnknownFlag(t *testing.T) {
	snap := rolloutSnapshot()
	_, err := Eval(snap, "no-such-flag", nil)
	var ee *Error
	if !errors.As(err, &ee) || ee.Reason != ReasonUnknownFlag {
		t.Fatalf("want ReasonUnknownFlag, got %v", err)
	}
	t.Logf("input={flag:no-such-flag} -> rejected reason=%s msg=%s", ee.Reason, ee.Message)
}

func TestEvalDefaultNoRollout(t *testing.T) {
	snap := rolloutSnapshot()
	d, err := Eval(snap, "plain-flag", nil)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	t.Logf("input={flag:plain-flag} -> value=%q reason=%s bucket=%d", d.Value.Str, d.Reason, d.Bucket)
	if d.Value.Str != "hello" || d.Reason != "default" || d.Bucket != -1 {
		t.Fatalf("unexpected decision %+v", d)
	}
}

// itoa 避免引入 strconv 之外的依赖噪音，简单够用。
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	p := len(buf)
	for i > 0 {
		p--
		buf[p] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[p:])
}
