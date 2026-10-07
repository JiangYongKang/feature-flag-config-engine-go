package engine

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// evalAll 对一批用户求值并记录完整结论，用于跨版本对比。
func evalAll(t *testing.T, e *Engine, flag string, users []string) map[string]Decision {
	t.Helper()
	out := make(map[string]Decision, len(users))
	for _, u := range users {
		d, err := e.Evaluate(flag, map[string]string{"user_id": u})
		if err != nil {
			t.Fatalf("eval %s user=%s: %v", flag, u, err)
		}
		out[u] = *d
	}
	return out
}

// TestRollbackRestoresVersionWithStableRollout 验证回滚后同样的属性输入
// 得到与目标版本刚发布时完全一致的结论（含灰度命中与分桶）。
func TestRollbackRestoresVersionWithStableRollout(t *testing.T) {
	src := NewMapSource("dynamic", 0, map[string]FlagDef{
		"flag-a": boolDef("flag-a", false),
		"flag-g": rolloutBoolDef("flag-g", 50),
	})
	e, err := NewWithOptions(Options{HistoryCapacity: 8}, src)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	users := make([]string, 200)
	for i := range users {
		users[i] = fmt.Sprintf("user-%d", i)
	}

	v1 := e.Snapshot().Version
	before := evalAll(t, e, "flag-g", users)
	hits := 0
	for _, d := range before {
		if d.Value.Bool {
			hits++
		}
	}
	t.Logf("v%d published: flag-g rollout=50%% hits=%d/%d", v1, hits, len(users))

	// 第二次发布：灰度比例与默认值都改变
	src.Set(rolloutBoolDef("flag-g", 10))
	src.Set(boolDef("flag-a", true))
	if err := e.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	v2 := e.Snapshot().Version
	mid := evalAll(t, e, "flag-g", users)
	diff := 0
	for _, u := range users {
		if mid[u].Value.Bool != before[u].Value.Bool {
			diff++
		}
	}
	t.Logf("v%d published: rollout 50%%->10%%, %d users changed result", v2, diff)
	if diff == 0 {
		t.Fatal("expected the second publish to change some rollout results")
	}

	// 回滚到 v1：所有用户的结论必须与 v1 刚发布时一致
	if err := e.Rollback(v1); err != nil {
		t.Fatalf("rollback to v%d: %v", v1, err)
	}
	v3 := e.Snapshot().Version
	after := evalAll(t, e, "flag-g", users)
	for _, u := range users {
		b, a := before[u], after[u]
		if a.Value != b.Value || a.Bucket != b.Bucket || a.Reason != b.Reason {
			t.Fatalf("user=%s: after rollback got %+v, want value/bucket/reason of %+v", u, a, b)
		}
	}
	t.Logf("rolled back v%d -> v%d (restoring content of v%d): all %d users match v%d conclusions (value+bucket+reason)",
		v2, v3, v1, len(users), v1)

	// 版本号必须单调递增，且回滚后的非灰度键也回到目标版本内容
	if v3 <= v2 {
		t.Fatalf("version must stay monotonic across rollback: v%d -> v%d", v2, v3)
	}
	d, err := e.Evaluate("flag-a", nil)
	if err != nil || d.Value.Bool {
		t.Fatalf("flag-a must be restored to v1 content (false), got %+v err=%v", d, err)
	}
	t.Logf("flag-a restored to v1 value=false at v%d (version monotonic)", v3)
}

// TestRollbackUnknownVersion 验证回滚目标不在历史中时按可区分原因拒绝，
// 当前生效版本不受影响。
func TestRollbackUnknownVersion(t *testing.T) {
	src := NewMapSource("dynamic", 0, map[string]FlagDef{"flag-a": boolDef("flag-a", true)})
	e, err := New(src)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	before := e.Snapshot()

	err = e.Rollback(999)
	var ee *Error
	if !errors.As(err, &ee) || ee.Reason != ReasonUnknownVersion {
		t.Fatalf("want ReasonUnknownVersion, got %v", err)
	}
	t.Logf("rollback to v999 rejected: reason=%s msg=%s", ee.Reason, ee.Message)
	if e.Snapshot() != before {
		t.Fatal("failed rollback must not touch the live snapshot")
	}
}

// TestHistoryCapacityEviction 验证历史容量满时淘汰最旧记录，
// 淘汰与回滚都不影响当前生效版本。
func TestHistoryCapacityEviction(t *testing.T) {
	src := NewMapSource("dynamic", 0, map[string]FlagDef{"flag-a": boolDef("flag-a", false)})
	e, err := NewWithOptions(Options{HistoryCapacity: 3}, src)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	// 再发布 4 次，共 5 条记录，容量 3 -> 最旧两条（v1、v2）被淘汰
	for i := 1; i <= 4; i++ {
		src.Set(boolDef("flag-a", i%2 == 0))
		if err := e.Reload(); err != nil {
			t.Fatalf("reload %d: %v", i, err)
		}
	}
	hist := e.History()
	var versions []uint64
	for _, s := range hist {
		versions = append(versions, s.Version)
	}
	t.Logf("capacity=3, published 5 versions, retained history versions=%v", versions)
	if len(hist) != 3 || versions[0] != 3 || versions[2] != 5 {
		t.Fatalf("want retained versions [3 4 5], got %v", versions)
	}

	// 被淘汰的版本不可回滚，且当前生效版本不受影响
	live := e.Snapshot()
	if err := e.Rollback(1); !errors.Is(err, &Error{Reason: ReasonUnknownVersion}) {
		var ee *Error
		if !errors.As(err, &ee) || ee.Reason != ReasonUnknownVersion {
			t.Fatalf("evicted version must be un-rollbackable, got %v", err)
		}
	}
	t.Logf("rollback to evicted v1 rejected, live snapshot still v%d", live.Version)
	if e.Snapshot() != live {
		t.Fatal("eviction/failed rollback must not affect the live snapshot")
	}

	// 仍在历史中的版本可以回滚
	if err := e.Rollback(3); err != nil {
		t.Fatalf("rollback to retained v3: %v", err)
	}
	d, err := e.Evaluate("flag-a", nil)
	if err != nil || !d.Value.Bool {
		t.Fatalf("v3 content (flag-a=true) must be restored, got %+v err=%v", d, err)
	}
	t.Logf("rollback to retained v3 ok: flag-a=%v at v%d", d.Value.Bool, d.Version)
}

// TestConcurrentReloadAndRollbackConsistency 验证并发发布与回滚下，
// 读者任意时刻看到的都是完整快照、版本号单调、同版本结论一致。
func TestConcurrentReloadAndRollbackConsistency(t *testing.T) {
	src := NewMapSource("dynamic", 0, map[string]FlagDef{
		"flag-g": rolloutBoolDef("flag-g", 50),
	})
	e, err := New(src)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	var readersWG, writersWG sync.WaitGroup
	stop := make(chan struct{})
	errCh := make(chan error, 64)

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

	// 写者：交替热更新与回滚（回滚目标取自当前历史，可能已被淘汰，允许失败）
	for w := 0; w < 4; w++ {
		writersWG.Add(1)
		go func(w int) {
			defer writersWG.Done()
			for i := 0; i < 100; i++ {
				src.Set(rolloutBoolDef("flag-g", float64((i*7+w)%100)))
				if err := e.Reload(); err != nil {
					errCh <- fmt.Errorf("writer %d reload: %w", w, err)
					return
				}
				if i%3 == 0 {
					hist := e.History()
					if len(hist) > 0 {
						target := hist[(i+w)%len(hist)].Version
						err := e.Rollback(target)
						// 目标可能在读取历史与回滚之间被其他写者淘汰，
						// UNKNOWN_VERSION 是合法结果；其他错误才是缺陷。
						var ee *Error
						if err != nil && !(errors.As(err, &ee) && ee.Reason == ReasonUnknownVersion) {
							errCh <- fmt.Errorf("writer %d rollback to v%d: %w", w, target, err)
							return
						}
					}
				}
			}
		}(w)
	}

	writersWG.Wait()
	close(stop)
	readersWG.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	t.Logf("concurrent reload+rollback finished: final version=%d, history=%d entries",
		e.Snapshot().Version, len(e.History()))
}
