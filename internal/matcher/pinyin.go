package matcher

import (
	"noblack/internal/normalize"
)

// 拼音自动机: 对抗用拼音替换汉字的绕过写法。
//
// "怎么做zha yào？谁有xǐnɡ dānɡ的渠道？" 归一化后是
// "怎么做zhayao谁有xingdang的渠道", 与词条 "炸药" 字面上毫无交集。
// 把两侧都转成拼音后, 输入含 "zhayao"、词条 "炸药" 也是 "zhayao", 即可命中。
//
// 只对满足 normalize.PinyinIndexable 的词条建索引 (拼音长度 >= 6 且含汉字),
// 短拼音歧义过大, 收录必然误报。

// pinyinAutomaton 是与主自动机并列的第二张自动机, 词条键为拼音。
type pinyinAutomaton struct {
	inner *Automaton
	// size 是实际建立索引的词条数, 用于日志与诊断。
	size int
}

// buildPinyinAutomaton 从词条集合构建拼音自动机。
// 无可索引词条时返回 nil, 调用方据此跳过拼音匹配。
func buildPinyinAutomaton(entries []Entry, caseInsensitive bool) *pinyinAutomaton {
	builder := NewBuilder(caseInsensitive)
	indexed := 0
	for _, entry := range entries {
		py, ok := normalize.PinyinIndexable(entry.Word)
		if !ok {
			continue
		}
		// 键是拼音, 但展示词保留原始词条 —— 命中结果对外展示的
		// 仍是词库里的原文, 而不是一串拼音。
		builder.AddAs(py, entry.Word, entry.Levels, entry.Remarks)
		indexed++
	}
	if indexed == 0 {
		return nil
	}
	return &pinyinAutomaton{inner: builder.Build(), size: indexed}
}

// pinyinMapping 记录归一化文本中每个 rune 在拼音串里的起止下标,
// 用于把拼音串上的命中位置换算回归一化文本的位置。
type pinyinMapping struct {
	pinyin string
	// starts[i] / ends[i] 是归一化文本第 i 个 rune 对应的拼音区间 [start, end)。
	starts []int
	ends   []int
}

// buildPinyinMapping 把归一化文本转成拼音, 同时记录逐 rune 的位置映射。
//
// 必须与 normalize.Pinyin 保持完全一致的转换规则, 否则映射会错位。
func buildPinyinMapping(normalized string) pinyinMapping {
	runes := []rune(normalized)
	mapping := pinyinMapping{
		starts: make([]int, len(runes)),
		ends:   make([]int, len(runes)),
	}
	var builder []byte
	for i, r := range runes {
		mapping.starts[i] = len(builder)
		builder = append(builder, []byte(normalize.Pinyin(string(r)))...)
		mapping.ends[i] = len(builder)
	}
	mapping.pinyin = string(builder)
	return mapping
}

// mapRange 把拼音串上的字节区间换算回归一化文本的 rune 区间。
//
// 拼音串是逐 rune 拼接的, 因此命中区间可能落在某个 rune 的拼音中间
// (例如输入 "xingdang" 命中 "xiong" 的一部分)。这里向外取整到完整的
// rune 边界, 保证高亮覆盖整个汉字而不是半个拼音。
func (m pinyinMapping) mapRange(start, end int) (int, int, bool) {
	if len(m.starts) == 0 || start < 0 || end <= start {
		return 0, 0, false
	}
	runeStart, runeEnd := -1, -1
	for i := range m.starts {
		// 与命中区间有交集的 rune 都纳入
		if m.ends[i] > start && m.starts[i] < end {
			if runeStart == -1 {
				runeStart = i
			}
			runeEnd = i + 1
		}
	}
	if runeStart == -1 {
		return 0, 0, false
	}
	return runeStart, runeEnd, true
}

// findAll 在文本中查找拼音命中, 返回的位置已换算回**原文** rune 下标。
func (p *pinyinAutomaton) findAll(text string) []Match {
	if p == nil || p.inner == nil {
		return nil
	}
	result := normalize.Normalize(text)
	if result.Text == "" {
		return nil
	}
	mapping := buildPinyinMapping(result.Text)
	if mapping.pinyin == "" {
		return nil
	}

	raw := p.inner.FindAll(mapping.pinyin)
	if len(raw) == 0 {
		return nil
	}

	matches := make([]Match, 0, len(raw))
	for _, m := range raw {
		// FindAll 返回的是 rune 下标, 而拼音串全是 ASCII, 两者等价。
		normStart, normEnd, ok := mapping.mapRange(m.Start, m.End)
		if !ok {
			continue
		}
		originalStart, originalEnd := result.MapRange(normStart, normEnd)
		if originalEnd <= originalStart {
			continue
		}
		m.Start = originalStart
		m.End = originalEnd
		matches = append(matches, m)
	}
	return matches
}

// Size 返回建立了拼音索引的词条数。
func (p *pinyinAutomaton) Size() int {
	if p == nil {
		return 0
	}
	return p.size
}
