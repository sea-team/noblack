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
	builder := make([]byte, 0, len(runes)*3)
	for i, r := range runes {
		mapping.starts[i] = len(builder)
		builder = normalize.AppendPinyinRune(builder, r)
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

// minLettersForPinyinScan 是触发拼音扫描所需的最少字母数。
//
// 拼音索引的键最短 MinPinyinLength (10) 个字母, 但输入侧是中英混排,
// 键的一部分可能由汉字转出的拼音补足, 所以输入里的字母可以少于 10。
//
// 实测:
//   - 拼音绕过样本 (qiangzhidanyao / qiang zhi dan yao / weijinwupin)
//     字母总数最少 11
//   - 日常英文缩写 (CEO / PDF / WiFi / iPhone / discuss / API+bug)
//     字母总数最多 7
//
// 取 8 落在两者之间: 挡掉全部日常缩写, 又给真实绕过留出余量。
const minLettersForPinyinScan = 8

// needsPinyinScan 判断文本是否可能命中拼音索引。
//
// 拼音索引的键全是小写 ASCII 字母 (zhayao、qiangzhidanyao), 因此:
//   - 纯中文输入不可能命中, 直接跳过 (真实流量的绝大多数)
//   - 字母太少也不可能凑出一个拼音键, 同样跳过 —— "他是CEO"、"用PS修图"
//     这类日常写法无需付出转拼音 + 二次匹配的代价
//
// 统计的是**字母总数**而非最长连续段: 绕过写法常被中文切碎
// ("怎么做zha yao谁有xing dang"), 最长段可能只有 3~4, 但总数足够。
func needsPinyinScan(normalized string) bool {
	letters := 0
	for _, r := range normalized {
		if r >= 'a' && r <= 'z' {
			letters++
			if letters >= minLettersForPinyinScan {
				return true
			}
		}
	}
	return false
}

// findAllNormalized 在已归一化的文本上查找拼音命中。
//
// 复用调用方已算好的 normalize.Result, 避免重复归一化 ——
// FindAllNormalized 紧接着调用本函数, 两次归一化是纯浪费。
// 返回的位置已换算回**原文** rune 下标。
func (p *pinyinAutomaton) findAllNormalized(result normalize.Result) []Match {
	if p == nil || p.inner == nil || result.Text == "" {
		return nil
	}
	// 快速跳过: 纯中文输入不可能匹配全字母的拼音键。
	if !needsPinyinScan(result.Text) {
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
