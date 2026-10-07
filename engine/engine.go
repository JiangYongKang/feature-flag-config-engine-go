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

	// mu 序列化所有发布路径（Reload / Rollback）：版本号分配、合并、
	// 快照切换与历史记录在同一临界区内完成，保证版本单调且读者
	// 任意时刻只看到完整快照。
	mu      sync.Mutex
	version atomic.Uint64

	history *History
}

// New 用给定来源构建引擎并完成首次加载；首次加载失败则构建失败。
// 历史保留条数使用 DefaultHistoryCapacity。
func New(sources ...Source) (*Engine, error) {
	return NewWithOptions(Options{HistoryCapacity: DefaultHistoryCapacity}, sources...)
}

// NewWithOptions 同 New，但允许调用方指定历史保留条数等选项。
// HistoryCapacity <= 0 时回退到 DefaultHistoryCapacity。
func NewWithOptions(opts Options, sources ...Source) (*Engine, error) {
	e := &Engine{sources: sources, history: NewHistory(opts.HistoryCapacity)}
	if err := e.Reload(); err != nil {
		return nil, err
	}
	return e, nil
}

// Reload 重新加载所有来源并原子切换快照；失败时旧快照继续生效。
// 成功的发布会作为一条历史记录保留。
func (e *Engine) Reload() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.publishLocked(e.sources)
}

// publishLocked 合并给定来源、切换快照并记录历史；调用方必须持有 e.mu。
// 合并失败时不产生任何副作用：旧快照、版本号、历史全部保持原样。
func (e *Engine) publishLocked(sources []Source) error {
	next := e.version.Load() + 1
	snap, err := Merge(next, sources)
	if err != nil {
		return err // 版本号未前进，旧快照继续生效
	}
	e.current.Store(snap) // 单次原子写，读者看到的是完整快照
	e.version.Store(next)
	e.history.Record(snap)
	return nil
}

// Evaluate 对当前快照求值。
func (e *Engine) Evaluate(flagKey string, attrs map[string]string) (*Decision, error) {
	return Eval(e.current.Load(), flagKey, attrs)
}

// Snapshot 返回当前快照（只读，调用方不得修改）。
func (e *Engine) Snapshot() *Snapshot {
	return e.current.Load()
}
