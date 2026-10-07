package engine

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// --- ContentSource：与 FileSource 同源的校验与拒绝原因 ---

func TestContentSourceLoadOK(t *testing.T) {
	c := &ContentSource{
		SrcName: "candidate", SrcPriority: 0,
		Content: `{"flags":[{"key":"f1","type":"bool","default":true}]}`,
	}
	defs, err := c.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	t.Logf("input={content: bool f1=true} -> loaded f1=%v", defs["f1"].Default.Bool)
	if !defs["f1"].Default.Bool {
		t.Fatal("f1 should be true")
	}
}

func TestContentSourceRejections(t *testing.T) {
	cases := []struct {
		name    string
		content string
		reason  Reason
	}{
		{"empty", "", ReasonSourceMissing},
		{"bad-json", `{not-json`, ReasonInvalidStructure},
		{"type-mismatch", `{"flags":[{"key":"f","type":"bool","default":"x"}]}`, ReasonTypeMismatch},
		{"bad-structure", `{"flags":[{"key":"f","type":"weird","default":1}]}`, ReasonInvalidStructure},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &ContentSource{SrcName: "cand", SrcPriority: 0, Content: tc.content}
			_, err := c.Load()
			var ee *Error
			if !errors.As(err, &ee) || ee.Reason != tc.reason {
				t.Fatalf("want %s, got %v", tc.reason, err)
			}
			t.Logf("input={content-case:%s} -> rejected reason=%s msg=%s",
				tc.name, ee.Reason, ee.Message)
		})
	}
}

// --- 试算：新增 / 删除 / 修改 / 不变 ---

func TestSimulateDiff(t *testing.T) {
	base := NewMapSource("base", 0, map[string]FlagDef{
		"keep":  boolDef("keep", false),
		"gone":  boolDef("gone", true),
		"tweak": boolDef("tweak", false),
	})
	e, err := New(base)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	cand := &ContentSource{SrcName: "candidate", SrcPriority: 0, Content: `{"flags":[
		{"key":"keep","type":"bool","default":false},
		{"key":"tweak","type":"bool","default":true},
		{"key":"fresh","type":"string","default":"hi"}
	]}`}
	rep, err := e.Simulate(SimulationOptions{}, cand)
	if err != nil {
		t.Fatalf("simulate: %v", err)
	}
	t.Logf("simulate base=v%d -> added=%v deleted=%v modified=%v unchanged=%v",
		rep.BaseVersion, rep.AddedKeys, rep.DeletedKeys, rep.ModifiedKeys, rep.UnchangedKeys)
	for _, ch := range rep.Changes {
		t.Logf("  change key=%s kind=%s", ch.Key, ch.Kind)
	}
	if fmt.Sprint(rep.AddedKeys) != "[fresh]" {
		t.Fatalf("added = %v", rep.AddedKeys)
	}
	if fmt.Sprint(rep.DeletedKeys) != "[gone]" {
		t.Fatalf("deleted = %v", rep.DeletedKeys)
	}
	if fmt.Sprint(rep.ModifiedKeys) != "[tweak]" {
		t.Fatalf("modified = %v", rep.ModifiedKeys)
	}
	if fmt.Sprint(rep.UnchangedKeys) != "[keep]" {
		t.Fatalf("unchanged = %v", rep.UnchangedKeys)
	}

	// 试算绝不影响线上：版本、快照指针、可读值都保持原样
	if e.Snapshot().Version != 1 {
		t.Fatalf("online version changed after simulate: %d", e.Snapshot().Version)
	}
	if _, err := e.Evaluate("fresh", nil); err == nil {
		t.Fatal("fresh must not exist online after simulate")
	} else {
		var ee *Error
		errors.As(err, &ee)
		t.Logf("online eval fresh after simulate -> rejected reason=%s (线上未被试算影响)", ee.Reason)
	}
	if d, err := e.Evaluate("gone", nil); err != nil || !d.Value.Bool {
		t.Fatalf("gone must remain online after simulate, got %v %v", d, err)
	}
}

// --- 试算灰度走向与探针命中 ---

func TestSimulateRolloutTrend(t *testing.T) {
	base := NewMapSource("base", 0, map[string]FlagDef{
		"ro": {Key: "ro", Type: TypeBool, Default: Value{Type: TypeBool},
			Rollout: &RolloutRule{Attribute: "user_id", Percentage: 0}},
	})
	e, _ := New(base)
	cand := &ContentSource{SrcName: "cand", SrcPriority: 0, Content: `{"flags":[
		{"key":"ro","type":"bool","default":false,"rollout":{"attribute":"user_id","percentage":100}}
	]}`}
	rep, err := e.Simulate(SimulationOptions{
		Probes: map[string][]string{"user_id": {"alice", "bob"}},
	}, cand)
	if err != nil {
		t.Fatalf("simulate: %v", err)
	}
	var ro *FlagChange
	for i := range rep.Changes {
		if rep.Changes[i].Key == "ro" {
			ro = &rep.Changes[i]
		}
	}
	if ro == nil || ro.Kind != ChangeModified || ro.Rollout == nil {
		t.Fatalf("ro should be modified with rollout impact: %+v", ro)
	}
	t.Logf("input={ro 0%% -> 100%% probes:alice,bob} direction=%s before=%.0f%% after=%.0f%%",
		ro.Rollout.Direction, ro.Rollout.BeforePercentage, ro.Rollout.AfterPercentage)
	if ro.Rollout.Direction != "range-expanded" {
		t.Fatalf("direction = %s", ro.Rollout.Direction)
	}
	for _, p := range ro.Rollout.Probes {
		hitBefore, hitAfter := p.BeforeHit != nil && *p.BeforeHit, p.AfterHit != nil && *p.AfterHit
		t.Logf("  probe %s=%s before-hit=%v after-hit=%v note=%s",
			p.AttrName, p.AttrValue, p.BeforeHit, p.AfterHit, p.Note)
		if hitBefore || !hitAfter {
			t.Fatalf("probe %s expected miss->hit", p.AttrValue)
		}
	}
}

// --- 试算沿用同一套拒绝原因 ---

func TestSimulateRejections(t *testing.T) {
	locked := boolDef("core", true)
	locked.LockOverride = true
	base := NewMapSource("base", 0, map[string]FlagDef{"core": locked})
	e, _ := New(base)

	// 越权覆盖锁定键 -> FORBIDDEN_OVERRIDE（候选组合 = 锁定 base + 高优先级候选）
	bad := &ContentSource{SrcName: "cand", SrcPriority: 10, Content: `{"flags":[
		{"key":"core","type":"bool","default":false}
	]}`}
	_, err := e.Simulate(SimulationOptions{}, base, bad)
	var ee *Error
	if !errors.As(err, &ee) || ee.Reason != ReasonForbiddenOverride {
		t.Fatalf("want FORBIDDEN_OVERRIDE, got %v", err)
	}
	t.Logf("simulate locked override -> rejected reason=%s msg=%s", ee.Reason, ee.Message)

	// 候选来源读不到 -> SOURCE_MISSING
	broken := NewMapSource("broken", 0, nil)
	broken.SetErr(errors.New("boom"))
	_, err = e.Simulate(SimulationOptions{}, broken)
	if !errors.As(err, &ee) || ee.Reason != ReasonSourceMissing {
		t.Fatalf("want SOURCE_MISSING, got %v", err)
	}
	t.Logf("simulate unavailable source -> rejected reason=%s msg=%s", ee.Reason, ee.Message)

	// 类型不匹配 -> TYPE_MISMATCH
	badType := &ContentSource{SrcName: "cand", SrcPriority: 0,
		Content: `{"flags":[{"key":"x","type":"bool","default":"no"}]}`}
	_, err = e.Simulate(SimulationOptions{}, badType)
	if !errors.As(err, &ee) || ee.Reason != ReasonTypeMismatch {
		t.Fatalf("want TYPE_MISMATCH, got %v", err)
	}
	t.Logf("simulate type mismatch -> rejected reason=%s msg=%s", ee.Reason, ee.Message)

	// 试算全部失败后线上仍为 v1
	if e.Snapshot().Version != 1 {
		t.Fatalf("online changed after failed simulations: v%d", e.Snapshot().Version)
	}
}

// --- 发布历史与回滚 ---

// rolloutDef 构造一个带灰度的 bool 开关。
func rolloutDef(key string, pct float64) FlagDef {
	return FlagDef{Key: key, Type: TypeBool, Default: Value{Type: TypeBool},
		Rollout: &RolloutRule{Attribute: "user_id", Percentage: pct}}
}

// rolloutHits 在给定快照定义下记录一批用户的命中，用于回滚前后的稳定性比对。
func rolloutHits(t *testing.T, e *Engine, key string, uids []string) map[string]bool {
	t.Helper()
	hits := make(map[string]bool, len(uids))
	for _, uid := range uids {
		d, err := e.Evaluate(key, map[string]string{"user_id": uid})
		if err != nil {
			t.Fatalf("eval %s: %v", uid, err)
		}
		hits[uid] = d.Value.Bool
	}
	return hits
}

func TestPublishHistoryAndRollback(t *testing.T) {
	src := NewMapSource("dynamic", 0, map[string]FlagDef{
		"ro": rolloutDef("ro", 50),
	})
	e, err := NewWithOptions(Options{HistoryLimit: 5}, src)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	uids := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		uids = append(uids, "user-"+itoa(i))
	}
	v1Hits := rolloutHits(t, e, "ro", uids)
	t.Logf("v1 published: ro=50%% sample-hits=%d/%d history=%d",
		countTrue(v1Hits), len(uids), len(e.History()))

	// 发布 v2：灰度 100%
	src.Set(rolloutDef("ro", 100))
	if err := e.Publish(src); err != nil {
		t.Fatalf("publish v2: %v", err)
	}
	v2Hits := rolloutHits(t, e, "ro", uids)
	t.Logf("v2 published: ro=100%% sample-hits=%d/%d", countTrue(v2Hits), len(uids))
	if countTrue(v2Hits) != len(uids) {
		t.Fatal("v2 must hit everyone")
	}

	// 回滚到 v1：内容与 v1 刚发布时一致，灰度命中必须完全一致
	rb, err := e.Rollback(1)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	t.Logf("rolled back to content of v1 -> new version=%d history=%v",
		rb.Version, historyVersions(e))
	v1Again := rolloutHits(t, e, "ro", uids)
	mismatch := 0
	for uid, want := range v1Hits {
		if v1Again[uid] != want {
			mismatch++
		}
	}
	t.Logf("post-rollback hit stability: mismatches=%d/%d (与 v1 刚发布时一致)", mismatch, len(uids))
	if mismatch != 0 {
		t.Fatalf("rollback changed rollout result for %d users", mismatch)
	}

	// 回滚目标版本不在历史中 -> VERSION_NOT_FOUND
	_, err = e.Rollback(999)
	var ee *Error
	if !errors.As(err, &ee) || ee.Reason != ReasonVersionNotFound {
		t.Fatalf("want VERSION_NOT_FOUND, got %v", err)
	}
	t.Logf("rollback v999 -> rejected reason=%s msg=%s", ee.Reason, ee.Message)
}

func TestRollbackRestoresFullSet(t *testing.T) {
	src := NewMapSource("dynamic", 0, map[string]FlagDef{
		"a": boolDef("a", false),
		"b": boolDef("b", false),
	})
	e, _ := New(src)

	// v2：整体替换为只含 a=true 的候选（b 随之删除）
	v2 := &ContentSource{SrcName: "v2", SrcPriority: 0,
		Content: `{"flags":[{"key":"a","type":"bool","default":true}]}`}
	if err := e.Publish(v2); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if _, err := e.Evaluate("b", nil); err == nil {
		t.Fatal("b should be gone at v2")
	}
	rb, err := e.Rollback(1)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if _, err := e.Evaluate("b", nil); err != nil {
		t.Fatalf("b must be restored after rollback: %v", err)
	}
	d, _ := e.Evaluate("a", nil)
	t.Logf("rolled back to v1 content (version now %d): a=%v b restored", rb.Version, d.Value.Bool)
	if d.Value.Bool {
		t.Fatal("a must be back to v1 value false")
	}
}

func TestHistoryEviction(t *testing.T) {
	src := NewMapSource("dynamic", 0, map[string]FlagDef{"a": boolDef("a", false)})
	e, _ := NewWithOptions(Options{HistoryLimit: 3}, src)
	for i := 1; i <= 5; i++ {
		src.Set(boolDef("a", i%2 == 0))
		if err := e.Publish(src); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}
	h := e.History()
	t.Logf("after 6 publishes with limit=3 -> retained versions=%v (最旧淘汰)", historyVersions(e))
	if len(h) != 3 {
		t.Fatalf("history len = %d, want 3", len(h))
	}
	if h[0].Version != 4 || h[1].Version != 5 || h[2].Version != 6 {
		t.Fatalf("eviction kept %v, want [4 5 6]", historyVersions(e))
	}
	// 被淘汰的 v1 不可回滚
	if _, err := e.Rollback(1); err == nil {
		t.Fatal("evicted v1 must not be rollbackable")
	}
	// 淘汰不影响当前生效版本
	d, _ := e.Evaluate("a", nil)
	t.Logf("after eviction: online v%d a=%v (淘汰不影响当前生效版)", e.Snapshot().Version, d.Value.Bool)
	if e.Snapshot().Version != 6 {
		t.Fatalf("current version = %d, want 6", e.Snapshot().Version)
	}
}

func TestDefaultHistoryLimit(t *testing.T) {
	src := NewMapSource("dynamic", 0, map[string]FlagDef{"a": boolDef("a", false)})
	e, _ := New(src)
	if e.historyLimit != DefaultHistoryLimit {
		t.Fatalf("default limit = %d, want %d", e.historyLimit, DefaultHistoryLimit)
	}
}

func countTrue(m map[string]bool) int {
	n := 0
	for _, v := range m {
		if v {
			n++
		}
	}
	return n
}

func historyVersions(e *Engine) []uint64 {
	h := e.History()
	vs := make([]uint64, len(h))
	for i, en := range h {
		vs[i] = en.Version
	}
	return vs
}

// --- 发布 / 回滚 / 读 高并发一致性 ---

func TestConcurrentPublishRollbackRead(t *testing.T) {
	src := NewMapSource("dynamic", 0, map[string]FlagDef{
		"a": boolDef("a", false),
		"g": rolloutDef("g", 50),
	})
	// 容量给足，保证并发期间所有发布版本都可回滚
	e, err := NewWithOptions(Options{HistoryLimit: 10000}, src)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	var readersWG, writersWG sync.WaitGroup
	stop := make(chan struct{})
	errCh := make(chan error, 256)

	// 读者：固定快照指针上重复求值必须一致；观察到的版本号必须单调
	for r := 0; r < 8; r++ {
		readersWG.Add(1)
		go func(r int) {
			defer readersWG.Done()
			uid := fmt.Sprintf("user-%d", r)
			var lastVersion uint64
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := e.Snapshot() // 拿到的必须是一个完整版本
				d1, err := Eval(snap, "g", map[string]string{"user_id": uid})
				if err != nil {
					errCh <- fmt.Errorf("reader %d: %w", r, err)
					return
				}
				for i := 0; i < 5; i++ {
					d2, err := Eval(snap, "g", map[string]string{"user_id": uid})
					if err != nil {
						errCh <- fmt.Errorf("reader %d repeat: %w", r, err)
						return
					}
					if d2.Value.Bool != d1.Value.Bool || d2.Bucket != d1.Bucket ||
						d2.Version != d1.Version {
						errCh <- fmt.Errorf("reader %d: snapshot v%d not self-consistent", r, snap.Version)
						return
					}
				}
				if lastVersion != 0 && d1.Version < lastVersion {
					errCh <- fmt.Errorf("reader %d: version went backwards %d -> %d",
						r, lastVersion, d1.Version)
					return
				}
				lastVersion = d1.Version
			}
		}(r)
	}

	// 写者：交替发布 true/false，并周期性回滚到 v1
	for w := 0; w < 4; w++ {
		writersWG.Add(1)
		go func(w int) {
			defer writersWG.Done()
			for i := 0; i < 150; i++ {
				cand := &ContentSource{
					SrcName:     fmt.Sprintf("cand-%d-%d", w, i),
					SrcPriority: 0,
					Content: fmt.Sprintf(`{"flags":[
						{"key":"a","type":"bool","default":%v},
						{"key":"g","type":"bool","default":false,"rollout":{"attribute":"user_id","percentage":50}}
					]}`, i%2 == 0),
				}
				if err := e.Publish(cand); err != nil {
					errCh <- fmt.Errorf("writer %d publish: %w", w, err)
					return
				}
				if i%25 == 0 {
					if _, err := e.Rollback(1); err != nil {
						errCh <- fmt.Errorf("writer %d rollback: %w", w, err)
						return
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
	t.Logf("concurrent publish/rollback/read finished: final version=%d history=%d",
		e.Snapshot().Version, len(e.History()))
}

// 老配置兼容：只配灰度比例的旧 JSON 内容仍能加载、试算、发布与求值。
func TestLegacyRolloutOnlyConfigCompat(t *testing.T) {
	legacy := `{"flags":[
		{"key":"g","type":"bool","default":false,"rollout":{"attribute":"user_id","percentage":25}}
	]}`
	src := &ContentSource{SrcName: "legacy", SrcPriority: 0, Content: legacy}
	e, err := New(src)
	if err != nil {
		t.Fatalf("new with legacy content: %v", err)
	}
	d, err := e.Evaluate("g", map[string]string{"user_id": "alice"})
	if err != nil {
		t.Fatalf("eval legacy: %v", err)
	}
	t.Logf("input={legacy rollout-only user_id:alice} -> hit=%v reason=%s bucket=%d",
		d.Value.Bool, d.Reason, d.Bucket)
	if d.Reason != "rollout-hit" && d.Reason != "rollout-miss" {
		t.Fatalf("unexpected reason %s", d.Reason)
	}
	// 同样内容的试算必须判定为 unchanged
	rep, err := e.Simulate(SimulationOptions{},
		&ContentSource{SrcName: "legacy-cand", SrcPriority: 0, Content: legacy})
	if err != nil {
		t.Fatalf("simulate legacy: %v", err)
	}
	if len(rep.ModifiedKeys) != 0 || len(rep.AddedKeys) != 0 || len(rep.DeletedKeys) != 0 {
		t.Fatalf("legacy re-simulate should be unchanged: %+v", rep)
	}
	t.Logf("legacy re-simulate -> unchanged=%v (老配置行为保持)", rep.UnchangedKeys)
}

// 失败的 Publish 既不切快照，也不能污染后续 Reload 使用的来源集。
func TestFailedPublishKeepsSourcesAndSnapshot(t *testing.T) {
	src := NewMapSource("dynamic", 0, map[string]FlagDef{"a": boolDef("a", true)})
	e, _ := New(src)

	// 候选把 key 类型搞错 -> TYPE_MISMATCH，发布拒绝
	bad := &ContentSource{SrcName: "bad", SrcPriority: 0,
		Content: `{"flags":[{"key":"a","type":"bool","default":"oops"}]}`}
	err := e.Publish(bad)
	var ee *Error
	if !errors.As(err, &ee) || ee.Reason != ReasonTypeMismatch {
		t.Fatalf("want TYPE_MISMATCH, got %v", err)
	}
	t.Logf("bad publish -> rejected reason=%s msg=%s", ee.Reason, ee.Message)

	if e.Snapshot().Version != 1 {
		t.Fatalf("snapshot advanced after failed publish: v%d", e.Snapshot().Version)
	}
	// 改旧来源后 Reload 必须仍按旧来源集生效，证明失败候选未污染来源
	src.Set(boolDef("a", false))
	if err := e.Reload(); err != nil {
		t.Fatalf("reload after failed publish: %v", err)
	}
	d, err := e.Evaluate("a", nil)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	t.Logf("reload after failed publish: a=%v version=%d (来源集未被污染)",
		d.Value.Bool, d.Version)
	if d.Value.Bool {
		t.Fatal("expected reloaded value false from original source")
	}
}
