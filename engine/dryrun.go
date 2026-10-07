package engine

import (
	"sort"
)

// ChangeKind 标识单个开关在候选配置中的变化类型。
type ChangeKind string

const (
	ChangeAdded   ChangeKind = "added"   // 候选新增
	ChangeRemoved ChangeKind = "removed" // 候选删除
	ChangeChanged ChangeKind = "changed" // 值、类型、灰度或锁定发生变化
)

// 灰度命中范围走向。
const (
	ShiftExpanded  = "expanded"  // 命中比例变大，原命中用户保持命中
	ShiftShrunk    = "shrunk"    // 命中比例变小
	ShiftUnchanged = "unchanged" // 命中范围不变
)

// FlagChange 描述一个开关从当前生效配置到候选配置的变化。
type FlagChange struct {
	Key  string
	Kind ChangeKind
	Old  *FlagDef // added 时为 nil
	New  *FlagDef // removed 时为 nil

	// RolloutShift 描述灰度命中范围的走向（expanded/shrunk/unchanged），
	// 仅当该键在两侧都是带灰度的 bool 开关时填写，否则为空。
	RolloutShift string
}

// DryRunReport 是一次发布前试算的结论；只读，不影响线上生效配置。
type DryRunReport struct {
	BaseVersion      uint64       // 对比基准（当前生效快照版本）
	CandidateVersion uint64       // 候选快照若发布会使用的版本号
	Changes          []FlagChange // 按 key 排序的全部变化
}

func (r *DryRunReport) filter(kind ChangeKind) []FlagChange {
	var out []FlagChange
	for _, c := range r.Changes {
		if c.Kind == kind {
			out = append(out, c)
		}
	}
	return out
}

// Added 返回候选新增的开关变化。
func (r *DryRunReport) Added() []FlagChange { return r.filter(ChangeAdded) }

// Removed 返回候选删除的开关变化。
func (r *DryRunReport) Removed() []FlagChange { return r.filter(ChangeRemoved) }

// Changed 返回值/灰度/锁定发生变化的开关变化。
func (r *DryRunReport) Changed() []FlagChange { return r.filter(ChangeChanged) }

// DryRun 用候选来源组合做一次发布前试算：按与正式合并完全相同的规则
// （同一 Merge：优先级、锁定键、校验、拒绝原因）合并候选来源，与当前
// 生效快照对比，输出新增/删除/变更及灰度命中范围走向。
//
// 试算只读当前快照，不修改引擎的任何状态（版本号、快照、历史均不变），
// 期间线上读写照常进行；候选内容非法时返回与正式合并一致的可区分
// 原因错误，绝不静默回退默认结果。
func (e *Engine) DryRun(candidate ...Source) (*DryRunReport, error) {
	base := e.current.Load()
	// 候选版本号仅为展示用途，不占用引擎版本计数。
	snap, err := Merge(base.Version+1, candidate)
	if err != nil {
		return nil, err
	}
	return &DryRunReport{
		BaseVersion:      base.Version,
		CandidateVersion: snap.Version,
		Changes:          diffSnapshots(base, snap),
	}, nil
}

// diffSnapshots 对比两个快照，产出按 key 排序的变化列表。
func diffSnapshots(old, new *Snapshot) []FlagChange {
	keys := make(map[string]struct{}, len(old.Flags)+len(new.Flags))
	for k := range old.Flags {
		keys[k] = struct{}{}
	}
	for k := range new.Flags {
		keys[k] = struct{}{}
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	var changes []FlagChange
	for _, k := range sorted {
		oldDef, hasOld := old.Flags[k]
		newDef, hasNew := new.Flags[k]
		switch {
		case !hasOld:
			def := newDef
			changes = append(changes, FlagChange{Key: k, Kind: ChangeAdded, New: &def})
		case !hasNew:
			def := oldDef
			changes = append(changes, FlagChange{Key: k, Kind: ChangeRemoved, Old: &def})
		case !flagDefEqual(oldDef, newDef):
			o, n := oldDef, newDef
			changes = append(changes, FlagChange{
				Key: k, Kind: ChangeChanged, Old: &o, New: &n,
				RolloutShift: rolloutShift(oldDef.Rollout, newDef.Rollout),
			})
		}
	}
	return changes
}

// flagDefEqual 判断两个开关定义是否完全一致（类型、默认值、灰度、锁定）。
func flagDefEqual(a, b FlagDef) bool {
	return a.Key == b.Key &&
		a.Type == b.Type &&
		a.Default == b.Default &&
		a.LockOverride == b.LockOverride &&
		rolloutEqual(a.Rollout, b.Rollout)
}

func rolloutEqual(a, b *RolloutRule) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// rolloutShift 计算灰度命中范围的走向；任一侧无灰度时返回空串。
// 分桶是确定性的且阈值随 percentage 单调，因此比例扩大时原命中用户
// 保持命中（expanded），缩小时部分原命中用户退出（shrunk）。
func rolloutShift(old, new *RolloutRule) string {
	if old == nil || new == nil {
		return ""
	}
	switch {
	case new.Percentage > old.Percentage:
		return ShiftExpanded
	case new.Percentage < old.Percentage:
		return ShiftShrunk
	default:
		return ShiftUnchanged
	}
}
