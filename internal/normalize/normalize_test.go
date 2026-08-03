package normalize

import "testing"

func TestNormalizeStripsNoise(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"插入英文句点", "炸.药", "炸药"},
		{"插入中文句号", "包。夜", "包夜"},
		{"插入空格", "炸 药", "炸药"},
		{"插入下划线", "炸_药", "炸药"},
		{"插入星号", "炸*药", "炸药"},
		{"插入多个符号", "炸.*-药", "炸药"},
		{"插入 Emoji", "炸🔥药", "炸药"},
		{"全角标点", "炸．药", "炸药"},
		{"零宽字符", "炸​药", "炸药"},
		{"正常文本不变", "炸药", "炸药"},
		{"保留数字字母", "c4炸药", "c4炸药"},
		{"大写转小写", "C4炸药", "c4炸药"},
		{"全角字母数字", "Ｃ４炸药", "c4炸药"},
		{"繁体转简体", "槍支", "枪支"},
		{"繁简混合加干扰", "槍.支", "枪支"},
		{"空串", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Text(tc.in); got != tc.want {
				t.Errorf("Text(%q) = %q, 期望 %q", tc.in, got, tc.want)
			}
		})
	}
}

// 索引映射必须把归一化文本上的命中换算回原文, 否则前端高亮会错位。
func TestMapRangeBackToOriginal(t *testing.T) {
	cases := []struct {
		name               string
		text               string
		start, end         int // 归一化文本上的区间
		wantStart, wantEnd int // 期望的原文区间
	}{
		// "炸.药" 归一化为 "炸药", 命中 [0,2) 应覆盖原文 [0,3) 含中间的点。
		{"插符号命中覆盖干扰字符", "炸.药", 0, 2, 0, 3},
		{"无干扰时区间一致", "炸药", 0, 2, 0, 2},
		{"带前缀", "这里有炸.药教程", 3, 5, 3, 6},
		{"多个干扰字符", "炸..药", 0, 2, 0, 4},
		{"末尾命中", "教程炸药", 2, 4, 2, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := Normalize(tc.text)
			gotStart, gotEnd := result.MapRange(tc.start, tc.end)
			if gotStart != tc.wantStart || gotEnd != tc.wantEnd {
				t.Errorf("MapRange(%d,%d) on %q = (%d,%d), 期望 (%d,%d); 归一化后=%q",
					tc.start, tc.end, tc.text, gotStart, gotEnd, tc.wantStart, tc.wantEnd, result.Text)
			}
		})
	}
}

// 越界输入不得 panic, 也不得返回越过原文长度的下标。
func TestMapRangeBoundsAreSafe(t *testing.T) {
	result := Normalize("炸.药")
	cases := [][2]int{{-1, 2}, {0, 0}, {5, 9}, {0, 99}, {2, 1}}
	for _, c := range cases {
		start, end := result.MapRange(c[0], c[1])
		if start < 0 || end < 0 || end > result.OriginalRunes {
			t.Errorf("MapRange(%d,%d) = (%d,%d) 越界; 原文 rune 数=%d",
				c[0], c[1], start, end, result.OriginalRunes)
		}
	}
	// 空输入不得 panic。
	if start, end := (Result{}).MapRange(0, 1); start != 0 || end != 0 {
		t.Errorf("空 Result 的 MapRange 应返回 (0,0), 得到 (%d,%d)", start, end)
	}
}

// 索引数组长度必须与归一化文本的 rune 数一致, 否则映射会错位。
func TestIndicesLengthMatchesText(t *testing.T) {
	for _, text := range []string{"炸.药", "这里有详细的炸.药简易制作教程", "c4炸药", "槍．支", ""} {
		result := Normalize(text)
		if len([]rune(result.Text)) != len(result.Indices) {
			t.Errorf("%q: 文本 rune 数=%d, 索引数=%d, 两者必须相等",
				text, len([]rune(result.Text)), len(result.Indices))
		}
		for i, index := range result.Indices {
			if index < 0 || index >= result.OriginalRunes {
				t.Errorf("%q: Indices[%d]=%d 超出原文范围 [0,%d)",
					text, i, index, result.OriginalRunes)
			}
		}
	}
}
