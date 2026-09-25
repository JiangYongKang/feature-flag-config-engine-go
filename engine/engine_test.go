package engine

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestHotUpdate(t *testing.T) {
	src := NewMapSource("dynamic", 0, map[string]FlagDef{
		"flag-a": boolDef("flag-a", false),
	})
	e, err := New(src)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	d, err := e.Evaluate("flag-a", nil)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	t.Logf("before update: flag-a=%v version=%d", d.Value.Bool, d.Version)
	if d.Value.Bool {
		t.Fatal("expected false before update")
	}

	// 热更新：修改来源后 Reload，读者应看到新快照
	src.Set(boolDef("flag-a", true))
	if err := e.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	d, err = e.Evaluate("flag-a", nil)
	if err != nil {
		t.Fatalf("eval after reload: %v", err)
	}
	t.Logf("after update: flag-a=%v version=%d", d.Value.Bool, d.Version)
	if !d.Value.Bool || d.Version != 2 {
		t.Fatalf("expected true at version 2, got %v v%d", d.Value.Bool, d.Version)
	}
}

func TestFailedReloadKeepsOldSnapshot(t *testing.T) {
	src := NewMapSource("dynamic", 0, map[string]FlagDef{
		"flag-a": boolDef("flag-a", true),
	})
	e, err := New(src)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	before := e.Snapshot()

	// 注入非法配置，Reload 必须失败且旧快照继续生效
	src.Set(FlagDef{Key: "flag-bad", Type: TypeBool,
		Default: Value{Type: TypeString, Str: "not-a-bool"}})
	err = e.Reload()
	var ee *Error
	if !errors.As(err, &ee) || ee.Reason != ReasonTypeMismatch {
		t.Fatalf("want ReasonTypeMismatch, got %v", err)
	}
	t.Logf("reload with invalid config -> rejected reason=%s msg=%s", ee.Reason, ee.Message)

	after := e.Snapshot()
	if after != before {
		t.Fatal("failed reload must not swap snapshot")
	}
	d, err := e.Evaluate("flag-a", nil)
	if err != nil || !d.Value.Bool {
		t.Fatalf("old config must remain effective, got %+v err=%v", d, err)
	}
	t.Logf("after failed reload: flag-a=%v version=%d (old snapshot preserved)", d.Value.Bool, d.Version)
}

func TestConcurrentReadWriteConsistency(t *testing.T) {
	src := NewMapSource("dynamic", 0, map[string]FlagDef{
		"flag-a": boolDef("flag-a", false),
		"flag-g": {
			Key: "flag-g", Type: TypeBool,
			Default: Value{Type: TypeBool},
			Rollout: &RolloutRule{Attribute: "user_id", Percentage: 50},
		},
	})
	e, err := New(src)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	var readersWG, writersWG sync.WaitGroup
	stop := make(chan struct{})
	errCh := make(chan error, 64)

	// 并发读者：每次求值必须基于某一个完整快照，且同一输入结论稳定
	for r := 0; r < 8; r++ {
		readersWG.Add(1)
		go func(r int) {
			defer readersWG.Done()
			uid := fmt.Sprintf("user-%d", r)
			var lastVersion uint64
			var lastVal bool
			for {
				select {
				case <-stop:
					return
				default:
				}
				d, err := e.Evaluate("flag-g", map[string]string{"user_id": uid})
				if err != nil {
					errCh <- fmt.Errorf("reader %d: %w", r, err)
					return
				}
				// 同一版本内结论必须一致；版本变化只允许来自完整切换
				if d.Version == lastVersion && d.Value.Bool != lastVal {
					errCh <- fmt.Errorf("reader %d: inconsistent value within version %d", r, d.Version)
					return
				}
				if lastVersion != 0 && d.Version < lastVersion {
					errCh <- fmt.Errorf("reader %d: version went backwards %d -> %d", r, lastVersion, d.Version)
					return
				}
				lastVersion, lastVal = d.Version, d.Value.Bool
			}
		}(r)
	}

	// 并发写者：交替热更新合法配置
	for w := 0; w < 4; w++ {
		writersWG.Add(1)
		go func(w int) {
			defer writersWG.Done()
			for i := 0; i < 200; i++ {
				src.Set(boolDef("flag-a", i%2 == 0))
				if err := e.Reload(); err != nil {
					errCh <- fmt.Errorf("writer %d: %w", w, err)
					return
				}
			}
		}(w)
	}

	writersWG.Wait()   // 写者全部完成
	close(stop)        // 通知读者退出
	readersWG.Wait()   // 等读者退出
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}
