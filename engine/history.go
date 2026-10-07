package engine

import "fmt"

// DefaultHistoryCapacity 是发布历史的默认保留条数。
const DefaultHistoryCapacity = 32

// Options 是引擎的可选配置。
type Options struct {
	// HistoryCapacity 历史保留条数；<= 0 时使用 DefaultHistoryCapacity。
	HistoryCapacity int
}

// History 按发布顺序保留最近的若干条快照，容量满时淘汰最旧的一条。
// 仅由 Engine 在持有发布锁时访问，自身不加锁。
type History struct {
	capacity int
	entries  []*Snapshot // 按版本升序，最旧在前
}

// NewHistory 创建指定容量的历史记录；capacity <= 0 时使用默认值。
func NewHistory(capacity int) *History {
	if capacity <= 0 {
		capacity = DefaultHistoryCapacity
	}
	return &History{capacity: capacity}
}

// Record 追加一条发布记录；容量满时淘汰最旧的一条。
// 淘汰只影响历史列表，绝不影响当前生效快照。
func (h *History) Record(snap *Snapshot) {
	h.entries = append(h.entries, snap)
	if len(h.entries) > h.capacity {
		// 淘汰最旧的一条；复制到新切片避免底层数组无限增长
		kept := make([]*Snapshot, h.capacity)
		copy(kept, h.entries[len(h.entries)-h.capacity:])
		h.entries = kept
	}
}

// Find 按版本号查找历史记录；未找到返回 nil。
func (h *History) Find(version uint64) *Snapshot {
	for _, s := range h.entries {
		if s.Version == version {
			return s
		}
	}
	return nil
}

// Entries 返回历史记录副本（按版本升序），供查询与测试。
func (h *History) Entries() []*Snapshot {
	out := make([]*Snapshot, len(h.entries))
	copy(out, h.entries)
	return out
}

// Rollback 将线上整体切回历史中的某一版。
// 切回后的快照内容与目标版本完全一致：同样的属性输入求值得到同样的
// 结论（含灰度命中结果）。为保持版本号单调递增，切回会分配一个新的
// 版本号，并作为一次成功发布记入历史。
// 目标版本不在历史中时按 ReasonUnknownVersion 拒绝，当前生效版本不受影响。
func (e *Engine) Rollback(version uint64) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	target := e.history.Find(version)
	if target == nil {
		return newError(ReasonUnknownVersion,
			fmt.Sprintf("version %d not found in history (capacity %d)",
				version, e.history.capacity))
	}
	// 用目标版本的完整内容构造新快照；Flags  map 不可变，共享是安全的。
	next := e.version.Load() + 1
	restored := &Snapshot{Version: next, Flags: target.Flags}
	e.current.Store(restored) // 单次原子写，读者要么看到旧版、要么看到完整的目标内容
	e.version.Store(next)
	e.history.Record(restored)
	return nil
}

// History 返回当前保留的发布历史（按版本升序的副本）。
func (e *Engine) History() []*Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.history.Entries()
}
