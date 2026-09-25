package engine

import (
	"sync/atomic"
)

// Engine 支持热更新的并发安全求值引擎。
// 快照通过 atomic.Pointer 整体切换，并发读取要么看到旧快照、要么看到新快照，
// 绝不出现部分生效。
type Engine struct {
	sources []Source
	current atomic.Pointer[Snapshot]
	version atomic.Uint64
}

// New 用给定来源构建引擎并完成首次加载；首次加载失败则构建失败。
func New(sources ...Source) (*Engine, error) {
	e := &Engine{sources: sources}
	if err := e.Reload(); err != nil {
		return nil, err
	}
	return e, nil
}

// Reload 重新加载所有来源并原子切换快照；失败时旧快照继续生效。
func (e *Engine) Reload() error {
	next := e.version.Add(1)
	snap, err := Merge(next, e.sources)
	if err != nil {
		e.version.Add(^uint64(0)) // 合并失败，回退版本号，保持单调
		return err
	}
	e.current.Store(snap) // 单次原子写，读者看到的是完整快照
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
