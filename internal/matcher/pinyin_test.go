package matcher

import (
	"strings"
	"testing"

	"noblack/internal/normalize"
)

// 拼音索引只在 Normalize + Pinyin 同时开启时建立。
func TestPinyinIndexRequiresBothOptions(t *testing.T) {
	entries := []Entry{{Word: "枪支弹药", Levels: []string{"High"}}}
	cases := []struct {
		name string
		opts Options
		want bool
	}{
		{"都不开", Options{}, false},
		{"仅归一化", Options{Normalize: true}, false},
		{"仅拼音", Options{Pinyin: true}, false},
		{"都开", Options{Normalize: true, Pinyin: true}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := BuildFromEntries(entries, tc.opts)
			if got := a.PinyinSize() > 0; got != tc.want {
				t.Errorf("拼音索引存在=%v, 期望 %v", got, tc.want)
			}
		})
	}
}

// 拼音写法应能命中对应词条, 且命中位置指向原文。
func TestPinyinMatchesBypassWriting(t *testing.T) {
	entries := []Entry{
		{Word: "枪支弹药", Levels: []string{"High"}},
		{Word: "违禁物品", Levels: []string{"High"}},
	}
	a := BuildFromEntries(entries, Options{Normalize: true, Pinyin: true})

	cases := []struct {
		name    string
		text    string
		word    string
		segment string // 期望高亮的原文片段
	}{
		{"纯拼音", "哪里能买qiangzhidanyao", "枪支弹药", "qiangzhidanyao"},
		{"带空格", "哪里能买qiang zhi dan yao", "枪支弹药", "qiang zhi dan yao"},
		{"带声调", "哪里能买qiāng zhī dàn yào", "枪支弹药", "qiāng zhī dàn yào"},
		{"字面命中", "哪里能买枪支弹药", "枪支弹药", "枪支弹药"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matches := a.FindAllNormalized(tc.text)
			var found *Match
			for i := range matches {
				if matches[i].Word == tc.word {
					found = &matches[i]
					break
				}
			}
			if found == nil {
				t.Fatalf("未命中 %q, 实际命中 %d 处", tc.word, len(matches))
			}
			runes := []rune(tc.text)
			if found.Start < 0 || found.End > len(runes) || found.End <= found.Start {
				t.Fatalf("命中位置越界: [%d,%d), 文本长度 %d", found.Start, found.End, len(runes))
			}
			if got := string(runes[found.Start:found.End]); got != tc.segment {
				t.Errorf("原文片段=%q, 期望 %q", got, tc.segment)
			}
		})
	}
}

// 短拼音不建索引: 同音词太多, 收录必然误报。
func TestPinyinSkipsShortWords(t *testing.T) {
	entries := []Entry{
		{Word: "炸药", Levels: []string{"High"}},   // zhayao, 6 字符 < 10
		{Word: "枪支弹药", Levels: []string{"High"}}, // qiangzhidanyao, 14 字符
	}
	a := BuildFromEntries(entries, Options{Normalize: true, Pinyin: true})
	if a.PinyinSize() != 1 {
		t.Fatalf("应只索引 1 条 (长拼音), 实际 %d 条", a.PinyinSize())
	}
	// 短词的拼音写法不会命中 —— 这是有意的取舍, 不是缺陷。
	if ms := a.FindAllNormalized("怎么做zhayao"); len(ms) != 0 {
		t.Errorf("短拼音不应建索引, 却命中了 %d 处", len(ms))
	}
	// 但字面写法照常命中。
	if ms := a.FindAllNormalized("怎么做炸药"); len(ms) != 1 {
		t.Errorf("字面写法应命中 1 处, 实际 %d 处", len(ms))
	}
}

// 纯英文/数字词条不建拼音索引: 其 "拼音" 就是自身, 会与主自动机重复。
func TestPinyinSkipsNonHanzi(t *testing.T) {
	entries := []Entry{
		{Word: "bilibili2233", Levels: []string{"Low"}},
		{Word: "枪支弹药", Levels: []string{"High"}},
	}
	a := BuildFromEntries(entries, Options{Normalize: true, Pinyin: true})
	if a.PinyinSize() != 1 {
		t.Errorf("应只索引含汉字的 1 条, 实际 %d 条", a.PinyinSize())
	}
}

// 同一位置被字面与拼音同时命中时, 不重复上报。
func TestPinyinDeduplicatesWithLiteral(t *testing.T) {
	entries := []Entry{{Word: "枪支弹药", Levels: []string{"High"}}}
	a := BuildFromEntries(entries, Options{Normalize: true, Pinyin: true})
	matches := a.FindAllNormalized("购买枪支弹药")
	count := 0
	for _, m := range matches {
		if m.Word == "枪支弹药" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("同一命中应只上报 1 次, 实际 %d 次", count)
	}
}

// 命中结果展示的是词库原文, 而不是拼音键。
func TestPinyinMatchShowsOriginalWord(t *testing.T) {
	entries := []Entry{{Word: "枪支弹药", Levels: []string{"High"}, Remarks: []string{"管制物品"}}}
	a := BuildFromEntries(entries, Options{Normalize: true, Pinyin: true})
	matches := a.FindAllNormalized("买qiangzhidanyao")
	if len(matches) != 1 {
		t.Fatalf("应命中 1 处, 实际 %d 处", len(matches))
	}
	if matches[0].Word != "枪支弹药" {
		t.Errorf("展示词=%q, 应为词库原文 枪支弹药", matches[0].Word)
	}
	if len(matches[0].Levels) != 1 || matches[0].Levels[0] != "High" {
		t.Errorf("等级应随词条带出, 实际 %v", matches[0].Levels)
	}
	if len(matches[0].Remarks) != 1 || matches[0].Remarks[0] != "管制物品" {
		t.Errorf("备注应随词条带出, 实际 %v", matches[0].Remarks)
	}
}

// 正常中文不应因拼音索引产生误报。这是拼音功能能否上线的底线。
func TestPinyinNoFalsePositiveOnNormalText(t *testing.T) {
	entries, err := LoadEntries("../../data/words.json", Options{})
	if err != nil {
		t.Skipf("跳过: 未找到真实词库 (%v)", err)
	}
	base := BuildFromEntries(entries, Options{Normalize: true})
	withPinyin := BuildFromEntries(entries, Options{Normalize: true, Pinyin: true})

	corpus := []string{
		"今天天气不错我们一起去公园散步吧",
		"这个产品的用户体验做得非常好推荐大家试试",
		"会议定在下周三上午十点请准时参加",
		"麻烦帮我查一下这个订单的物流状态谢谢",
		"最近在学习编程感觉还挺有意思的",
		"我想去西安旅游看兵马俑和大雁塔",
		"孩子今年上小学一年级成绩还可以",
		"公司年会安排在下个月中旬举行",
		"新买的手机拍照效果特别清晰",
		"医生说要多喝水注意休息",
		"下周要出差去上海参加展会",
		"她准备考研复习得很辛苦",
	}
	for _, text := range corpus {
		baseCount := len(base.FindAllNormalized(text))
		pinyinCount := len(withPinyin.FindAllNormalized(text))
		if pinyinCount > baseCount {
			var extras []string
			for _, m := range withPinyin.FindAllNormalized(text) {
				runes := []rune(text)
				extras = append(extras, m.Word+"<-"+string(runes[m.Start:m.End]))
			}
			t.Errorf("正常文本 %q 因拼音索引新增命中 (%d->%d): %s",
				text, baseCount, pinyinCount, strings.Join(extras, ", "))
		}
	}
}

// 阈值是误报防线, 不应被无意调低。
func TestMinPinyinLengthGuard(t *testing.T) {
	if normalize.MinPinyinLength < 8 {
		t.Errorf("MinPinyinLength=%d 低于实测安全线 8, 会引入大量同音词误报",
			normalize.MinPinyinLength)
	}
}

// 快速跳过必须只跳过不可能命中的输入, 不能漏掉真实的拼音绕过。
func TestPinyinFastPathCorrectness(t *testing.T) {
	entries := []Entry{{Word: "枪支弹药", Levels: []string{"High"}}}
	a := BuildFromEntries(entries, Options{Normalize: true, Pinyin: true})

	cases := []struct {
		name string
		text string
		want bool // 是否应命中
	}{
		{"纯中文-无字母-应跳过", "今天天气不错我们去公园散步", false},
		{"纯中文-含词库词-字面命中", "购买枪支弹药", true},
		{"中英混排-拼音绕过", "哪里买qiangzhidanyao", true},
		{"大写拼音绕过", "哪里买QIANGZHIDANYAO", true},
		{"带声调拼音绕过", "哪里买qiāngzhīdànyào", true},
		{"含无关英文-不应命中", "今天开会discuss项目进度", false},
		{"含数字-不应命中", "订单号12345已发货", false},
		{"日常英文缩写-字母太少应跳过", "他是CEO用PS修图", false},
		{"英文单词-不应命中", "这个API接口有bug需要fix", false},
		{"空文本", "", false},
		{"纯标点", "！？。，", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := len(a.FindAllNormalized(tc.text)) > 0
			if got != tc.want {
				t.Errorf("命中=%v, 期望 %v", got, tc.want)
			}
		})
	}
}

// needsPinyinScan 的判据本身。
func TestNeedsPinyinScan(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"今天天气不错", false},         // 纯中文
		{"", false},               // 空
		{"12345", false},          // 纯数字
		{"ceo", false},            // 3 字母, 低于阈值
		{"apibug", false},         // 6 字母, 低于阈值
		{"discuss", false},        // 7 字母, 低于阈值 (日常英文上界)
		{"qiangzhi", true},        // 8 字母, 达到阈值
		{"qiangzhidanyao", true},  // 14 字母
		{"怎么做zhayaoshenme", true}, // 中英混排, 字母足够
		{"我在cbd上班用ps修图", false},   // 混排但字母总数仅 5
	}
	for _, tc := range cases {
		if got := needsPinyinScan(tc.text); got != tc.want {
			t.Errorf("needsPinyinScan(%q)=%v, 期望 %v", tc.text, got, tc.want)
		}
	}
}

// 逐 rune 映射产出的拼音串必须与整串转换完全一致, 否则位置换算会错位。
func TestPinyinMappingMatchesWholeString(t *testing.T) {
	for _, text := range []string{
		"今天天气不错",
		"怎么做zhayao谁有xingdang",
		"混合ABC123中文",
		"qiāngzhīdànyào",
		"！？。，符号",
	} {
		mapping := buildPinyinMapping(text)
		if want := normalize.Pinyin(text); mapping.pinyin != want {
			t.Errorf("文本 %q: 逐rune=%q, 整串=%q", text, mapping.pinyin, want)
		}
	}
}
