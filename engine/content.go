package engine

import (
	"encoding/json"
	"fmt"
)

// ContentSource 直接以 JSON 文本作为候选配置内容，用于发布前试算或临时发布。
// 内容格式与 FileSource 相同；内容为空、类型不符、结构非法都会按
// 与 FileSource 一致的可区分原因拒绝（复用同一解析与校验路径）。
type ContentSource struct {
	SrcName     string
	SrcPriority int
	Content     string
}

func (c *ContentSource) Name() string  { return c.SrcName }
func (c *ContentSource) Priority() int { return c.SrcPriority }

func (c *ContentSource) Load() (map[string]FlagDef, error) {
	if c.Content == "" {
		return nil, newError(ReasonSourceMissing,
			fmt.Sprintf("source %q: empty content", c.SrcName))
	}
	return parseFlagDoc(c.SrcName, []byte(c.Content))
}

// parseFlagDoc 解析并校验一份 JSON 配置文档，是 FileSource 与 ContentSource
// 共用的唯一解析路径，保证两条入口的拒绝原因与结构校验完全一致。
func parseFlagDoc(srcName string, data []byte) (map[string]FlagDef, error) {
	var doc fileDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, newError(ReasonInvalidStructure,
			fmt.Sprintf("source %q: parse: %v", srcName, err))
	}
	out := make(map[string]FlagDef, len(doc.Flags))
	for _, ff := range doc.Flags {
		def, err := flagFromFile(ff)
		if err != nil {
			return nil, fmt.Errorf("source %q: %w", srcName, err)
		}
		if _, dup := out[def.Key]; dup {
			return nil, newError(ReasonInvalidStructure,
				fmt.Sprintf("source %q: duplicate flag key %q", srcName, def.Key))
		}
		out[def.Key] = def
	}
	return out, nil
}
