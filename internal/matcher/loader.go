package matcher

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Options 控制词库加载与自动机构建行为。
type Options struct {
	// CaseInsensitive 为 true 时匹配大小写不敏感, 主要惠及英文词
	// (如 "Bilibili" / "BILIBILI" 都能命中词条 "bilibili")。
	CaseInsensitive bool
	// DefaultLevel 用于未显式标注等级的词条。为空则回退到 "Low"。
	DefaultLevel string
	// Normalize 为 true 时, 词条与输入都会先做归一化 (去标点/空白/零宽字符,
	// 繁简、全半角、大小写折叠), 用于对抗 "炸.药" 这类插入字符的绕过手段。
	Normalize bool
	// Pinyin 为 true 时额外建立拼音索引, 用于对抗 "zha yào" 这类
	// 用拼音替换汉字的绕过手段。只索引拼音长度 >= 6 且含汉字的词条,
	// 短拼音歧义过大 (xing 可对应几十个字) 必然误报。
	//
	// 拼音匹配依赖归一化 (声调折叠在归一化阶段完成), 因此 Normalize
	// 为 false 时该项无效。
	Pinyin bool
}

// Entry 是词库中的一个词条 (归一化后的内部/对外表示)。
// 与 wordEntry 的区别: Entry 的字段已是干净的字符串切片, 供 CRUD 与保存使用。
type Entry struct {
	Word    string   `json:"word"`
	Levels  []string `json:"levels"`
	Remarks []string `json:"remarks"`
}

// wordEntry 是 JSON 词库文件里的单条结构 (解析用, 做了字段兼容)。
//
// 等级字段双写兼容:
//   - "levels": ["bilibili","引流"]   (推荐: 多等级)
//   - "level":  "High"                 (兼容: 单等级, 也可写 "a,b" 逗号串)
//
// 备注字段同理, 既支持数组也支持逗号分隔字符串。
type wordEntry struct {
	Word    string       `json:"word"`
	Levels  stringOrList `json:"levels"`
	Level   stringOrList `json:"level"`
	Remarks stringOrList `json:"remarks"`
}

// jsonWordFile 是 JSON 词库的顶层结构 (对象形式)。
type jsonWordFile struct {
	Words []Entry `json:"words"`
}

// LoadFromFile 读取 JSON 词库并构建全新 Automaton。
// 不涉及共享状态, 可在后台 goroutine 调用。
func LoadFromFile(path string, opts Options) (*Automaton, error) {
	entries, err := LoadEntries(path, opts)
	if err != nil {
		return nil, err
	}
	return BuildFromEntries(entries, opts), nil
}

// DefaultLevel 是词条未显式标注等级时使用的等级。
//
// 词库文件加载与 API 新增/修改都回落到它, 保证同一条词条无论从哪个入口
// 进来都有等级 —— 空等级的词条在列表筛选与统计归类里都会掉出来。
const DefaultLevel = "Low"

// LoadEntries 读取并解析 JSON 词库文件, 返回归一化后的词条列表 (不构建自动机)。
// CRUD 接口用它拿到当前全部词条。
func LoadEntries(path string, opts Options) ([]Entry, error) {
	if opts.DefaultLevel == "" {
		opts.DefaultLevel = DefaultLevel
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取词库文件失败: %w", err)
	}
	entries, err := parseEntries(data, opts.DefaultLevel)
	if err != nil {
		return nil, err
	}
	if err := ValidateEntries(entries, opts); err != nil {
		return nil, fmt.Errorf("词库内容无效: %w", err)
	}
	return entries, nil
}

// parseEntries 解析 JSON 字节流为归一化词条列表。
// 兼容两种顶层形态: {"words":[...]} 或 裸数组 [...]。
func parseEntries(data []byte, defaultLevel string) ([]Entry, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, nil
	}

	var raw []wordEntry
	if trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &raw); err != nil {
			return nil, fmt.Errorf("解析 JSON 数组词库失败: %w", err)
		}
	} else {
		// 顶层对象。为兼容 level/levels 双写, 这里用 wordEntry 重新走一遍。
		var f struct {
			Words []wordEntry `json:"words"`
		}
		if err := json.Unmarshal(trimmed, &f); err != nil {
			return nil, fmt.Errorf("解析 JSON 词库失败: %w", err)
		}
		raw = f.Words
	}

	entries := make([]Entry, 0, len(raw))
	for _, e := range raw {
		// 归一化 word: 拆分逗号 -> 去空段/空白 -> 重新拼接。
		// 这样即使文件被手工编辑出脏数据 (如 "  ,  ,TS,  "), 加载后也是干净的。
		word := NormalizeWord(e.Word)
		if word == "" {
			continue
		}
		levels := []string(e.Levels)
		if len(levels) == 0 {
			levels = []string(e.Level)
		}
		if len(levels) == 0 {
			levels = []string{defaultLevel}
		}
		entries = append(entries, Entry{
			Word:    word,
			Levels:  cleanStrings(levels),
			Remarks: cleanStrings([]string(e.Remarks)),
		})
	}
	return entries, nil
}

// ValidateEntries 检查按逗号展开后的敏感词是否唯一。
// 例如 "a,b" 和 "a" 展开后会发生重复；若不拒绝，后录入词条会静默覆盖自动机中的元数据。
// 启用大小写不敏感时会按折叠后的词比较，因此 "A" 和 "a" 也视为重复。
func ValidateEntries(entries []Entry, opts Options) error {
	seen := make(map[string]string)
	for _, e := range entries {
		for _, word := range SplitWords(e.Word) {
			key := word
			if opts.CaseInsensitive {
				key = strings.Map(func(r rune) rune { return fold(r, true) }, word)
			}
			if previous, ok := seen[key]; ok {
				return fmt.Errorf("展开后的敏感词 %q 重复，分别来自词条 %q 和 %q", word, previous, e.Word)
			}
			seen[key] = e.Word
		}
	}
	return nil
}

// BuildFromEntries 用词条列表构建只读自动机。单个词条可包含多个逗号分隔的敏感词，
// 这些词共享相同的等级和备注。
func BuildFromEntries(entries []Entry, opts Options) *Automaton {
	b := NewBuilder(opts.CaseInsensitive)
	if opts.Normalize {
		b = NewNormalizedBuilder(opts.CaseInsensitive)
	}
	// 拼音索引用拆分后的单词构建, 与主自动机保持同一批词。
	var expanded []Entry
	for _, e := range entries {
		for _, w := range SplitWords(e.Word) {
			b.Add(w, e.Levels, e.Remarks)
			if opts.Normalize && opts.Pinyin {
				expanded = append(expanded, Entry{Word: w, Levels: e.Levels, Remarks: e.Remarks})
			}
		}
	}
	automaton := b.Build()
	if opts.Normalize && opts.Pinyin {
		automaton.pinyin = buildPinyinAutomaton(expanded, opts.CaseInsensitive)
	}
	return automaton
}

// SplitWords 把词条的 word 字段按中英文逗号拆成多个独立敏感词, 去空白去空项。
// 单个词 (不含逗号) 时返回只含它自己的切片。
func SplitWords(word string) []string {
	if !strings.ContainsAny(word, ",，") {
		w := strings.TrimSpace(word)
		if w == "" {
			return nil
		}
		return []string{w}
	}
	return splitByComma(word)
}

// NormalizeWord 归一化 word 字段: 拆分 -> 去每段空白/空段 -> 用英文逗号重新拼接。
// 例如 "  ,  ,TS,  " -> "TS", "扶她, 伪娘 ,男同" -> "扶她,伪娘,男同"。
// 全为空白/逗号时返回空串 (调用方据此拒绝)。
func NormalizeWord(word string) string {
	return strings.Join(SplitWords(word), ",")
}

// NormalizeEntry 归一化整个词条: word 去脏, levels/remarks 去空白去空项。
// 供 CRUD 写入前统一清洗, 保证落盘的数据干净。
func NormalizeEntry(e Entry) Entry {
	levels := cleanStrings(e.Levels)
	// 未标注等级时回落到 DefaultLevel, 与词库文件加载时的行为一致 ——
	// 否则通过 API 新增的词条会是空等级, 在列表与统计里都无法归类。
	if len(levels) == 0 {
		levels = []string{DefaultLevel}
	}
	return Entry{
		Word:    NormalizeWord(e.Word),
		Levels:  levels,
		Remarks: cleanStrings(e.Remarks),
	}
}

// SaveEntries 将词条列表以规范 JSON ({"words":[...]}) 写入文件。
// 先写临时文件再原子 rename, 避免写到一半被读到半个文件。
func SaveEntries(path string, entries []Entry) error {
	if err := ValidateEntries(entries, Options{}); err != nil {
		return fmt.Errorf("词库内容无效: %w", err)
	}
	// 归一化, 保证 levels/remarks 为非 nil 切片 (JSON 输出 [] 而非 null)。
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		w := strings.TrimSpace(e.Word)
		if w == "" {
			continue
		}
		out = append(out, Entry{
			Word:    w,
			Levels:  cleanStrings(e.Levels),
			Remarks: cleanStrings(e.Remarks),
		})
	}

	data, err := json.MarshalIndent(jsonWordFile{Words: out}, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化词库失败: %w", err)
	}
	data = append(data, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("写入临时词库文件失败: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("替换词库文件失败: %w", err)
	}
	return nil
}

// splitByComma 按中英文逗号切分, 去空白去空项。
func splitByComma(inner string) []string {
	fields := strings.FieldsFunc(inner, func(r rune) bool {
		return r == ',' || r == '，'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// stringOrList 允许 JSON 字段既可为字符串数组, 也可为逗号分隔的单字符串。
type stringOrList []string

func (s *stringOrList) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		*s = nil
		return nil
	}
	if data[0] == '[' {
		var arr []string
		if err := json.Unmarshal(data, &arr); err != nil {
			return err
		}
		*s = arr
		return nil
	}
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return err
	}
	*s = splitByComma(str)
	return nil
}
