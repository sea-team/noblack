// Package normalize 把用户输入还原为标准形式, 用于对抗变体字绕过。
//
// 黑产常用手段: 在敏感词中间插入标点/空白/Emoji (炸.药、萝 莉), 或使用繁体字
// (槍支)。这些变体在字面上与词库不匹配, 也会削弱模型的判别信号。
// 在进入词库匹配和模型推理之前统一还原, 能直接解决大部分漏报。
//
// 关键约束: 归一化会改变字符下标, 而 API 返回的命中位置必须指向原文。
// 因此 Normalize 同时产出 rune 级索引映射, 供调用方把命中位置换算回原文。
package normalize

import (
	"strings"
	"unicode"
)

// Result 是归一化的产物。
type Result struct {
	// Text 是归一化后的文本 (已去除干扰字符, 繁体转简体, 全角转半角, 小写化)。
	Text string
	// Indices[i] 是 Text 中第 i 个 rune 在原文中的 rune 下标。
	// 用于把归一化文本上的命中位置换算回原文位置。
	Indices []int
	// OriginalRunes 是原文的 rune 数量, 供越界保护使用。
	OriginalRunes int
}

// MapRange 把归一化文本上的 [start, end) 区间换算回原文的 rune 区间。
//
// 由于干扰字符被剔除, 原文区间通常比归一化区间更宽:
// "炸.药" 归一化为 "炸药", 命中 [0,2) 换算回原文即 [0,3) —— 含中间那个点,
// 这样前端高亮才能覆盖完整的变体写法。
func (r Result) MapRange(start, end int) (int, int) {
	if len(r.Indices) == 0 || start < 0 || end <= start {
		return 0, 0
	}
	if start >= len(r.Indices) {
		return 0, 0
	}
	if end > len(r.Indices) {
		end = len(r.Indices)
	}
	originalStart := r.Indices[start]
	// end 是开区间, 取最后一个字符的原文下标 +1。
	originalEnd := r.Indices[end-1] + 1
	if originalEnd > r.OriginalRunes {
		originalEnd = r.OriginalRunes
	}
	return originalStart, originalEnd
}

// isNoise 判断一个 rune 是否为应当剔除的干扰字符。
//
// 剔除范围: 标点、符号、空白、控制字符、Emoji 等。
// 保留范围: 汉字、字母、数字 —— 这些是词库词条的组成部分, 剔除会导致误匹配。
func isNoise(r rune) bool {
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return false
	}
	// unicode.Cf 是格式字符 (零宽空格 U+200B、零宽连接符、软连字符等)。
	// 这类字符不可见, 是最隐蔽的绕过手段, 必须剔除。
	return unicode.IsPunct(r) || unicode.IsSymbol(r) ||
		unicode.IsSpace(r) || unicode.IsControl(r) || unicode.IsMark(r) ||
		unicode.Is(unicode.Cf, r)
}

// Normalize 归一化文本并产出索引映射。
//
// 处理顺序 (逐 rune, 保证 1:1 或 1:0, 索引映射始终精确):
//  1. 全角转半角 —— Ｎ→N、０→0, 覆盖全角数字/字母/标点。
//  2. 繁体转简体 —— 槍支→枪支。
//  3. 剔除干扰字符 —— 炸.药→炸药, 含标点/空白/Emoji/控制符。
//  4. 小写化 —— 大小写变体归一。
//
// 不引入 x/text 依赖: 这里只做全角→半角的宽度折叠, 而非完整 NFKC。
// 完整 NFKC 会把 ㈠ 展开成 (一) 等一对多映射, 对本场景收益有限,
// 却会让索引映射复杂化。宽度折叠已覆盖绝大多数变体写法。
func Normalize(text string) Result {
	if text == "" {
		return Result{}
	}
	originalRunes := []rune(text)
	var builder strings.Builder
	indices := make([]int, 0, len(originalRunes))

	for originalIndex, original := range originalRunes {
		converted := toSimplified(foldWidth(original))
		if isNoise(converted) {
			continue
		}
		builder.WriteRune(unicode.ToLower(converted))
		indices = append(indices, originalIndex)
	}

	return Result{
		Text:          builder.String(),
		Indices:       indices,
		OriginalRunes: len(originalRunes),
	}
}

// foldWidth 把全角字符折叠为半角。
//
// 全角 ASCII (Ａ-Ｚ ａ-ｚ ０-９ 及全角标点) 位于 U+FF01..U+FF5E,
// 与半角 U+0021..U+007E 一一对应, 相差固定偏移 0xFEE0。
// 全角空格 U+3000 单独处理 (会被 isNoise 剔除, 这里保持语义完整)。
func foldWidth(r rune) rune {
	switch {
	case r == '　':
		return ' '
	case r >= '！' && r <= '～':
		return r - 0xFEE0
	default:
		return r
	}
}

// Text 是只需要归一化文本时的便捷入口 (例如送给模型服务)。
func Text(text string) string {
	return Normalize(text).Text
}
