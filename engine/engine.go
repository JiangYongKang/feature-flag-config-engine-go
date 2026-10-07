package engine

import (
	"sync"
	"sync/atomic"
)

// Engine 支持热更新的并发安全求值引擎。
// 快照通过 atomic.Pointer 整体切换，并发读取要么看到旧快照、要么看到新快照，
// 绝不出现部分生效。
type Engine struct {
	sources []Source
	current atomic.Pointer[Snapshot]
	version atomic.Uint64

	// mu 保护 sources 的替换与 history 的追加/淘汰；读者走 atomic 无锁路径。
	mu           sync.Mutex
	history      []HistoryEntry // 按发布先后排列，最旧在前
	historyLimit int
}

// New 用给定来源构建引擎并完成首次加载；首次加载失败则构建失败。
func New(sources ...Source) (*Engine, error) {
	return NewWithOptions(Options{}, sources...)
}

// Reload 重新加载所有来源并原子切换快照；失败时旧快照继续生效。
func (e *Engine) Reload() error {
	e.mu.Lock()
	sources := append([]Source(nil), e.sources...)
	// publishLocked 会接管 e.mu，这里先释放。
	e.mu.Unlock()
	return e.publish(sources, false)
}

// Evaluate 对当前快照求值。
func (e *Engine) Evaluate(flagKey string, attrs map[string]string) (*Decision, error) {
	return Eval(e.current.Load(), flagKey, attrs)
}

// Snapshot 返回当前快照（只读，调用方不得修改）。
func (e *Engine) Snapshot() *Snapshot {
	return e.current.Load()
}

// publish 合并给定来源并原子提交；persist 为 true 时同时把来源组合记为
// 引擎当前来源（供后续 Reload 使用）。整个过程与其他发布/回滚串行执行，
// 保证版本号分配顺序与提交顺序一致（版本号对读者单调）；合并失败不动快照、
// 不前进版本号。读者始终走 atomic 无锁路径，不受此锁影响。
func (e *Engine) publish(sources []Source, persist bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.publishLocked(sources, persist)
}

func (e *Engine) publishLocked(sources []Source, persist bool) error {
	next := e.version.Add(1)
	snap, err := Merge(next, sources)
	if err != nil {
		e.version.Add(^uint64(0)) // 合并失败，回退版本号，保持单调
		return err
	}
	// 合并成功后才把候选来源记为当前来源，保证失败发布不影响后续 Reload。
	if persist {
		e.sources = append([]Source(nil), sources...)
	}
	e.commitLocked(snap)
	return nil
}

// commitLocked 原子切换当前快照并追加一条历史，超出上限时淘汰最旧记录。
// 淘汰只动历史切片，绝不触碰刚提交或此前生效的快照。调用方须持有 e.mu。
func (e *Engine) commitLocked(snap *Snapshot) {
	e.current.Store(snap) // 单次原子写，读者看到的是完整快照
	e.history = append(e.history, HistoryEntry{
		Version:     snap.Version,
		Snapshot:    snap,
		PublishedAt: now(),
	})
	if e.historyLimit > 0 && len(e.history) > e.historyLimit {
		e.history = e.history[len(e.history)-e.historyLimit:]
	}
}

// cloneFlags 深拷贝一份开关表，使回滚产出的快照独立于历史记录持有的快照。
func cloneFlags(in map[string]FlagDef) map[string]FlagDef {
	out := make(map[string]FlagDef, len(in))
	for k, v := range in {
		if v.Rollout != nil {
			r := *v.Rollout
			v.Rollout = &r
		}
		out[k] = v
	}
	return out
}
