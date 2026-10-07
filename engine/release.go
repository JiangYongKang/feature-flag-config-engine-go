package engine

import (
	"fmt"
	"sort"
	"time"
)

// DefaultHistoryLimit 是发布历史保留条数的默认上限；超出后淘汰最旧记录。
const DefaultHistoryLimit = 20

// ChangeKind 标识单个开关在候选快照相对当前快照时的变化类型。
type ChangeKind string

const (
	ChangeAdded     ChangeKind = "added"     // 候选中新增
	ChangeDeleted   ChangeKind = "deleted"   // 当前存在、候选中缺失（发布后会被删除）
	ChangeModified  ChangeKind = "modified"  // 键仍在，值或灰度范围发生变化
	ChangeUnchanged ChangeKind = "unchanged" // 无变化
)

// FlagChange 描述单个开关在试算中的差异结论。
type FlagChange struct {
	Key    string
	Kind   ChangeKind
	Before *FlagDef // 变化前定义；新增时为 nil
	After  *FlagDef // 变化后定义；删除时为 nil
	// Rollout 仅对 bool 开关的灰度差异给出走向；无灰度或灰度未变时为 nil。
	Rollout *RolloutImpact
}

// RolloutImpact 描述灰度范围变化及给定样本属性的命中走向。
type RolloutImpact struct {
	BeforeAttribute  string  // 变化前灰度属性，无灰度为空
	AfterAttribute   string  // 变化后灰度属性，无灰度为空
	BeforePercentage float64 // 变化前比例，无灰度为 0
	AfterPercentage  float64 // 变化后比例，无灰度为 0
	Direction        string  // 灰度走向，见 classifyRollout
	Probes           []ProbeResult
}

// ProbeResult 是某个样本属性在变化前后的灰度命中对比。
type ProbeResult struct {
	AttrName  string
	AttrValue string
	BeforeHit *bool // 变化前是否命中；变化前无灰度或属性不适用时为 nil
	AfterHit  *bool // 变化后是否命中；变化后无灰度或属性不适用时为 nil
	Note      string
}

// SimulationOptions 控制试算行为；试算绝不触碰线上快照。
type SimulationOptions struct {
	// Probes 为灰度走向提供样本属性输入，形如 {"user_id": ["alice", "bob"]}。
	Probes map[string][]string
}

// SimulationReport 是一次发布前试算的结论。
type SimulationReport struct {
	BaseVersion   uint64 // 试算时线上版本（不受影响）
	Candidate     *Snapshot
	Changes       []FlagChange // 按 key 排序，包含全部键（含 unchanged）
	AddedKeys     []string
	DeletedKeys   []string
	ModifiedKeys  []string
	UnchangedKeys []string
}

// HistoryEntry 是一条成功发布的历史记录；淘汰与回滚都不改动它。
type HistoryEntry struct {
	Version     uint64
	Snapshot    *Snapshot
	PublishedAt time.Time
}

// Options 是引擎的可选项。
type Options struct {
	// HistoryLimit 指定发布历史保留条数上限；<=0 时使用 DefaultHistoryLimit。
	HistoryLimit int
}

func now() time.Time { return time.Now() }

// NewWithOptions 用可选项构建引擎并完成首次加载；首次加载失败则构建失败。
// 首次加载成功的快照同样作为第一条发布历史保留，可直接用于回滚。
func NewWithOptions(o Options, sources ...Source) (*Engine, error) {
	limit := o.HistoryLimit
	if limit <= 0 {
		limit = DefaultHistoryLimit
	}
	e := &Engine{
		sources:      append([]Source(nil), sources...),
		historyLimit: limit,
	}
	if err := e.Reload(); err != nil {
		return nil, err
	}
	return e, nil
}

// Publish 按候选来源组合执行一次正式发布：复用同一套合并/校验/锁定规则，
// 成功后原子切换快照、把候选来源记为当前来源并追加历史；失败时线上不动。
func (e *Engine) Publish(sources ...Source) error {
	return e.publish(sources, true)
}

// Simulate 用候选来源组合试算：在不触碰线上快照、不影响任何线上读写的前提下，
// 复用 Merge 的全部规则（优先级、锁定键、来源缺失/类型/结构拒绝原因）产出候选快照，
// 并给出相对当前线上快照的新增/删除/修改/不变结论与灰度命中走向。
func (e *Engine) Simulate(opts SimulationOptions, sources ...Source) (*SimulationReport, error) {
	base := e.current.Load()
	// 候选版本号仅用于报告可读性与错误信息，不提交、不占用正式版本号。
	cand, err := Merge(base.Version+1, sources)
	if err != nil {
		return nil, err
	}
	return diffSnapshots(base, cand, opts.Probes), nil
}

// Rollback 把线上整体原子切回历史中指定版本对应的内容，并校验/锁定语义一致：
// 回滚产物与该版本当初发布时是同一份开关表，相同属性输入求值结论（含灰度命中）
// 完全一致。回滚通过单次原子写提交，读者任何时刻读到的都是一个完整版本。
// 回滚成功会作为一次新发布追加历史；目标版本不在历史中时以 VERSION_NOT_FOUND 拒绝。
func (e *Engine) Rollback(version uint64) (*Snapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var target *Snapshot
	for i := range e.history {
		if e.history[i].Version == version {
			target = e.history[i].Snapshot
			break
		}
	}
	if target == nil {
		return nil, newError(ReasonVersionNotFound,
			fmt.Sprintf("version %d not found in publish history", version))
	}
	next := e.version.Add(1)
	// 深拷贝开关表，避免历史快照与回滚快照共享可变底层结构。
	snap := &Snapshot{Version: next, Flags: cloneFlags(target.Flags)}
	e.commitLocked(snap)
	return snap, nil
}

// History 返回发布历史的只读副本（按发布先后，最旧在前）。
func (e *Engine) History() []HistoryEntry {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]HistoryEntry, len(e.history))
	copy(out, e.history)
	return out
}

// diffSnapshots 比较两个快照并按 key 排序产出逐键差异；灰度走向附带探针对比。
func diffSnapshots(base, cand *Snapshot, probes map[string][]string) *SimulationReport {
	rep := &SimulationReport{
		BaseVersion: base.Version,
		Candidate:   cand,
	}
	keySet := make(map[string]struct{}, len(base.Flags)+len(cand.Flags))
	for k := range base.Flags {
		keySet[k] = struct{}{}
	}
	for k := range cand.Flags {
		keySet[k] = struct{}{}
	}
	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		bDef, bOK := base.Flags[k]
		cDef, cOK := cand.Flags[k]
		ch := FlagChange{Key: k}
		switch {
		case !bOK && cOK:
			ch.Kind, ch.After = ChangeAdded, ptrFlag(cDef)
			rep.AddedKeys = append(rep.AddedKeys, k)
		case bOK && !cOK:
			ch.Kind, ch.Before = ChangeDeleted, ptrFlag(bDef)
			rep.DeletedKeys = append(rep.DeletedKeys, k)
		case equalFlagDef(bDef, cDef):
			ch.Kind = ChangeUnchanged
			ch.Before, ch.After = ptrFlag(bDef), ptrFlag(cDef)
			rep.UnchangedKeys = append(rep.UnchangedKeys, k)
		default:
			ch.Kind = ChangeModified
			ch.Before, ch.After = ptrFlag(bDef), ptrFlag(cDef)
			rep.ModifiedKeys = append(rep.ModifiedKeys, k)
		}
		if bOK && cOK && (!equalFlagDef(bDef, cDef)) {
			if imp := rolloutImpact(k, bDef, cDef, probes); imp != nil {
				ch.Rollout = imp
			}
		}
		rep.Changes = append(rep.Changes, ch)
	}
	return rep
}

func ptrFlag(d FlagDef) *FlagDef {
	cp := d
	if d.Rollout != nil {
		r := *d.Rollout
		cp.Rollout = &r
	}
	return &cp
}

// equalFlagDef 判断两份定义（值与灰度范围）是否完全一致。
func equalFlagDef(a, b FlagDef) bool {
	if a.Key != b.Key || a.Type != b.Type || a.LockOverride != b.LockOverride {
		return false
	}
	if !equalValue(a.Type, a.Default, b.Default) {
		return false
	}
	if (a.Rollout == nil) != (b.Rollout == nil) {
		return false
	}
	if a.Rollout != nil {
		if a.Rollout.Attribute != b.Rollout.Attribute ||
			a.Rollout.Percentage != b.Rollout.Percentage {
			return false
		}
	}
	return true
}

func equalValue(t ValueType, x, y Value) bool {
	switch t {
	case TypeBool:
		return x.Bool == y.Bool
	case TypeString:
		return x.Str == y.Str
	case TypeNumber:
		return x.Num == y.Num
	case TypeJSON:
		return x.JSON == y.JSON
	}
	return false
}

// rolloutImpact 在灰度范围或开关默认值发生变化时给出走向；无灰度语义变化返回 nil。
func rolloutImpact(key string, before, after FlagDef, probes map[string][]string) *RolloutImpact {
	hasB, hasA := before.Rollout != nil, after.Rollout != nil
	if !hasB && !hasA {
		return nil
	}
	imp := &RolloutImpact{}
	if hasB {
		imp.BeforeAttribute, imp.BeforePercentage = before.Rollout.Attribute, before.Rollout.Percentage
	}
	if hasA {
		imp.AfterAttribute, imp.AfterPercentage = after.Rollout.Attribute, after.Rollout.Percentage
	}
	switch {
	case !hasB && hasA:
		imp.Direction = "rollout-added"
	case hasB && !hasA:
		imp.Direction = "rollout-removed"
	case imp.BeforeAttribute != imp.AfterAttribute:
		imp.Direction = "attribute-changed"
	case imp.AfterPercentage > imp.BeforePercentage:
		imp.Direction = "range-expanded"
	case imp.AfterPercentage < imp.BeforePercentage:
		imp.Direction = "range-shrunk"
	default:
		imp.Direction = "unchanged"
	}
	imp.Probes = probeRollout(key, before, after, probes)
	return imp
}

// probeRollout 对样本属性逐一比较变化前后的灰度命中。
func probeRollout(key string, before, after FlagDef, probes map[string][]string) []ProbeResult {
	attrs := sortedProbeAttrs(probes)
	var out []ProbeResult
	for _, attr := range attrs {
		for _, val := range probes[attr] {
			p := ProbeResult{AttrName: attr, AttrValue: val}
			p.BeforeHit, p.Note = probeHit(key, before, attr, val)
			ah, aNote := probeHit(key, after, attr, val)
			p.AfterHit = ah
			switch {
			case p.BeforeHit != nil && p.AfterHit != nil && *p.BeforeHit != *p.AfterHit:
				p.Note = "hit-status-changed"
			case aNote != "" && p.Note == "":
				p.Note = aNote
			}
			out = append(out, p)
		}
	}
	return out
}

func sortedProbeAttrs(probes map[string][]string) []string {
	attrs := make([]string, 0, len(probes))
	for a := range probes {
		attrs = append(attrs, a)
	}
	sort.Strings(attrs)
	return attrs
}

// probeHit 计算单份定义对样本的命中：无灰度返回 (nil,"no-rollout")；
// 灰度属性与样本属性不匹配返回 (nil,"attribute-not-applicable")；
// 属性为空返回 (nil,"empty-attribute")；否则返回命中布尔。
func probeHit(key string, def FlagDef, attr, val string) (*bool, string) {
	if def.Rollout == nil {
		return nil, "no-rollout"
	}
	if def.Rollout.Attribute != attr {
		return nil, "attribute-not-applicable"
	}
	if val == "" {
		return nil, "empty-attribute"
	}
	bucket := bucketOf(key, val)
	hit := bucket < int(def.Rollout.Percentage*100)
	return &hit, ""
}
