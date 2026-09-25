package engine

import (
	"fmt"
	"sort"
)

// validateFlagDef 校验来自任意来源（含内存来源）的开关定义。
func validateFlagDef(srcName string, def FlagDef) error {
	if def.Key == "" {
		return newError(ReasonInvalidStructure,
			fmt.Sprintf("source %q: flag missing key", srcName))
	}
	switch def.Type {
	case TypeBool, TypeString, TypeNumber, TypeJSON:
	default:
		return newError(ReasonInvalidStructure,
			fmt.Sprintf("source %q: flag %q: unknown type %q", srcName, def.Key, def.Type))
	}
	if def.Default.Type != def.Type {
		return newError(ReasonTypeMismatch,
			fmt.Sprintf("source %q: flag %q: default type %q does not match declared type %q",
				srcName, def.Key, def.Default.Type, def.Type))
	}
	if def.Rollout != nil {
		if def.Type != TypeBool {
			return newError(ReasonInvalidStructure,
				fmt.Sprintf("source %q: flag %q: rollout only allowed on bool flags", srcName, def.Key))
		}
		if def.Rollout.Attribute == "" || def.Rollout.Percentage < 0 || def.Rollout.Percentage > 100 {
			return newError(ReasonInvalidStructure,
				fmt.Sprintf("source %q: flag %q: invalid rollout rule %+v", srcName, def.Key, *def.Rollout))
		}
	}
	return nil
}

// Merge 按优先级合并多层来源，产出不可变快照。
// 规则：
//   - Priority 数值越大优先级越高，高优先级来源的同名键覆盖低优先级；
//   - 低优先级来源中 LockOverride=true 的键被锁定，高优先级来源再定义该键
//     视为越权覆盖，整体合并按 ReasonForbiddenOverride 拒绝；
//   - 任一来源加载失败或定义非法，整体合并失败，调用方保留旧快照。
func Merge(version uint64, sources []Source) (*Snapshot, error) {
	sorted := make([]Source, len(sources))
	copy(sorted, sources)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Priority() < sorted[j].Priority()
	})

	merged := make(map[string]FlagDef)
	for _, src := range sorted {
		defs, err := src.Load()
		if err != nil {
			return nil, err // 已带可区分原因
		}
		for key, def := range defs {
			if def.Key == "" {
				def.Key = key
			}
			if err := validateFlagDef(src.Name(), def); err != nil {
				return nil, err
			}
			if existing, ok := merged[key]; ok && existing.LockOverride {
				return nil, newError(ReasonForbiddenOverride,
					fmt.Sprintf("source %q (priority %d) cannot override locked flag %q",
						src.Name(), src.Priority(), key))
			}
			merged[key] = def
		}
	}
	return &Snapshot{Version: version, Flags: merged}, nil
}
