package api

import (
	"net/http"
	"testing"

	"noblack/internal/matcher"
	"noblack/internal/stats"
	"noblack/internal/store"
)

// newNormalizedHandler 构造启用归一化的 Handler, 词库含 "炸药"。
func newNormalizedHandler(t *testing.T) *Handler {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/words.json"
	entries := []matcher.Entry{
		{Word: "炸药", Levels: []string{"违法"}},
		{Word: "枪支", Levels: []string{"违法"}},
	}
	if err := matcher.SaveEntries(path, entries); err != nil {
		t.Fatal(err)
	}
	options := matcher.Options{Normalize: true}
	return NewHandler(store.New(path, entries, options), stats.New(), "")
}

// 变体绕过必须被拦截: 这是本轮修复的核心场景。
func TestNormalizedMatchingBlocksVariants(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"原样命中", "这里有炸药教程", true},
		{"插入英文句点", "这里有炸.药教程", true},
		{"插入中文句号", "这里有炸。药教程", true},
		{"插入空格", "这里有炸 药教程", true},
		{"插入下划线", "这里有炸_药教程", true},
		{"插入星号", "这里有炸*药教程", true},
		{"插入多个符号", "这里有炸.-*药教程", true},
		{"插入零宽字符", "这里有炸​药教程", true},
		{"全角标点", "这里有炸．药教程", true},
		{"繁体写法", "這裡有槍支", true},
		{"繁体加干扰", "這裡有槍.支", true},
		// 正常文本不得误拦。
		{"正常文本", "今天天气不错，我们去公园散步", false},
		{"含标点的正常文本", "价格是99.5元，包邮到家", false},
		{"仅单字不成词", "这里有药品说明", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newNormalizedHandler(t)
			body := `{"text":` + quote(tc.text) + `}`
			data := decodeCheck(t, do(h, http.MethodPost, "/check", body, nil))
			if data.Blocked != tc.want {
				t.Errorf("blocked = %v, 期望 %v; text=%q matches=%+v",
					data.Blocked, tc.want, tc.text, data.Matches)
			}
		})
	}
}

// 命中位置必须指向原文, 且覆盖被剔除的干扰字符, 否则前端高亮会错位。
func TestNormalizedMatchPositionsMapToOriginal(t *testing.T) {
	h := newNormalizedHandler(t)
	// "这里有炸.药教程": 炸 在 rune 下标 3, 点在 4, 药在 5 → 期望 [3,6)
	data := decodeCheck(t, do(h, http.MethodPost, "/check", `{"text":"这里有炸.药教程"}`, nil))
	if len(data.Matches) != 1 {
		t.Fatalf("命中数 = %d, 期望 1; %+v", len(data.Matches), data.Matches)
	}
	match := data.Matches[0]
	if match.Word != "炸药" {
		t.Errorf("命中词 = %q, 期望 炸药", match.Word)
	}
	if match.Position.Start != 3 || match.Position.End != 6 {
		t.Errorf("位置 = [%d,%d), 期望 [3,6) 以覆盖中间的干扰字符",
			match.Position.Start, match.Position.End)
	}
	// 用原文按 rune 切片验证该区间确实是变体写法。
	runes := []rune("这里有炸.药教程")
	if got := string(runes[match.Position.Start:match.Position.End]); got != "炸.药" {
		t.Errorf("区间对应原文 = %q, 期望 炸.药", got)
	}
}

// 未启用归一化时保持原有字面匹配行为 (向后兼容)。
func TestLiteralMatchingUnchangedWhenNormalizeDisabled(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/words.json"
	entries := []matcher.Entry{{Word: "炸药", Levels: []string{"违法"}}}
	if err := matcher.SaveEntries(path, entries); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(store.New(path, entries, matcher.Options{}), stats.New(), "")

	if data := decodeCheck(t, do(h, http.MethodPost, "/check", `{"text":"炸药"}`, nil)); !data.Blocked {
		t.Error("字面命中应当拦截")
	}
	if data := decodeCheck(t, do(h, http.MethodPost, "/check", `{"text":"炸.药"}`, nil)); data.Blocked {
		t.Error("未启用归一化时不应命中变体 (保持原有行为)")
	}
}

// quote 把字符串转为 JSON 字面量, 避免手写转义出错。
func quote(s string) string {
	const hex = "0123456789abcdef"
	out := make([]rune, 0, len(s)+2)
	out = append(out, '"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			out = append(out, '\\', r)
		case r < 0x20:
			out = append(out, '\\', 'u', '0', '0',
				rune(hex[(r>>4)&0xf]), rune(hex[r&0xf]))
		default:
			out = append(out, r)
		}
	}
	return string(append(out, '"'))
}
