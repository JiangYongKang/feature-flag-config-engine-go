package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// Source 是一层配置来源。Priority 数值越大优先级越高。
type Source interface {
	Name() string
	Priority() int
	Load() (map[string]FlagDef, error)
}

// MapSource 内存来源，便于测试与运行时注入；并发安全。
type MapSource struct {
	SrcName     string
	SrcPriority int

	mu    sync.RWMutex
	flags map[string]FlagDef
	err   error // 非空时模拟来源加载失败
}

// NewMapSource 创建内存来源。
func NewMapSource(name string, priority int, flags map[string]FlagDef) *MapSource {
	m := &MapSource{SrcName: name, SrcPriority: priority}
	m.flags = make(map[string]FlagDef, len(flags))
	for k, v := range flags {
		m.flags[k] = v
	}
	return m
}

// Set 更新或新增一个开关定义（并发安全）。
func (m *MapSource) Set(def FlagDef) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flags[def.Key] = def
}

// SetErr 模拟来源故障（并发安全）；传 nil 恢复。
func (m *MapSource) SetErr(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

func (m *MapSource) Name() string  { return m.SrcName }
func (m *MapSource) Priority() int { return m.SrcPriority }

func (m *MapSource) Load() (map[string]FlagDef, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.err != nil {
		return nil, newError(ReasonSourceMissing,
			fmt.Sprintf("source %q unavailable: %v", m.SrcName, m.err))
	}
	out := make(map[string]FlagDef, len(m.flags))
	for k, v := range m.flags {
		out[k] = v
	}
	return out, nil
}

// fileFlag 是 JSON 文件中单个开关的编码形式。
type fileFlag struct {
	Key          string          `json:"key"`
	Type         string          `json:"type"`
	Default      json.RawMessage `json:"default"`
	Rollout      *RolloutRule    `json:"rollout,omitempty"`
	LockOverride bool            `json:"lock_override,omitempty"`
}

type fileDoc struct {
	Flags []fileFlag `json:"flags"`
}

// FileSource 从 JSON 文件加载配置。
type FileSource struct {
	SrcName     string
	SrcPriority int
	Path        string
}

func (f *FileSource) Name() string  { return f.SrcName }
func (f *FileSource) Priority() int { return f.SrcPriority }

func (f *FileSource) Load() (map[string]FlagDef, error) {
	data, err := os.ReadFile(f.Path)
	if err != nil {
		return nil, newError(ReasonSourceMissing,
			fmt.Sprintf("source %q: read %s: %v", f.SrcName, f.Path, err))
	}
	var doc fileDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, newError(ReasonInvalidStructure,
			fmt.Sprintf("source %q: parse %s: %v", f.SrcName, f.Path, err))
	}
	out := make(map[string]FlagDef, len(doc.Flags))
	for _, ff := range doc.Flags {
		def, err := flagFromFile(ff)
		if err != nil {
			return nil, fmt.Errorf("source %q: %w", f.SrcName, err)
		}
		if _, dup := out[def.Key]; dup {
			return nil, newError(ReasonInvalidStructure,
				fmt.Sprintf("source %q: duplicate flag key %q", f.SrcName, def.Key))
		}
		out[def.Key] = def
	}
	return out, nil
}

// flagFromFile 校验并转换文件中的开关定义；类型不匹配或结构非法按可区分原因拒绝。
func flagFromFile(ff fileFlag) (FlagDef, error) {
	if ff.Key == "" {
		return FlagDef{}, newError(ReasonInvalidStructure, "flag missing key")
	}
	def := FlagDef{Key: ff.Key, LockOverride: ff.LockOverride}
	switch ValueType(ff.Type) {
	case TypeBool:
		var b bool
		if err := json.Unmarshal(ff.Default, &b); err != nil {
			return FlagDef{}, newError(ReasonTypeMismatch,
				fmt.Sprintf("flag %q: default is not a bool", ff.Key))
		}
		def.Type, def.Default = TypeBool, Value{Type: TypeBool, Bool: b}
	case TypeString:
		var s string
		if err := json.Unmarshal(ff.Default, &s); err != nil {
			return FlagDef{}, newError(ReasonTypeMismatch,
				fmt.Sprintf("flag %q: default is not a string", ff.Key))
		}
		def.Type, def.Default = TypeString, Value{Type: TypeString, Str: s}
	case TypeNumber:
		var n float64
		if err := json.Unmarshal(ff.Default, &n); err != nil {
			return FlagDef{}, newError(ReasonTypeMismatch,
				fmt.Sprintf("flag %q: default is not a number", ff.Key))
		}
		def.Type, def.Default = TypeNumber, Value{Type: TypeNumber, Num: n}
	case TypeJSON:
		var anyv any
		if err := json.Unmarshal(ff.Default, &anyv); err != nil {
			return FlagDef{}, newError(ReasonTypeMismatch,
				fmt.Sprintf("flag %q: default is not valid JSON", ff.Key))
		}
		def.Type, def.Default = TypeJSON, Value{Type: TypeJSON, JSON: string(ff.Default)}
	default:
		return FlagDef{}, newError(ReasonInvalidStructure,
			fmt.Sprintf("flag %q: unknown type %q", ff.Key, ff.Type))
	}
	if ff.Rollout != nil {
		r := *ff.Rollout
		if def.Type != TypeBool {
			return FlagDef{}, newError(ReasonInvalidStructure,
				fmt.Sprintf("flag %q: rollout only allowed on bool flags", ff.Key))
		}
		if r.Attribute == "" || r.Percentage < 0 || r.Percentage > 100 {
			return FlagDef{}, newError(ReasonInvalidStructure,
				fmt.Sprintf("flag %q: invalid rollout rule %+v", ff.Key, r))
		}
		def.Rollout = &r
	}
	return def, nil
}
