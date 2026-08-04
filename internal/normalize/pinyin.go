package normalize

import "unicode/utf8"

// MinPinyinLength 是参与拼音匹配的最小拼音长度。
//
// 拼音匹配天然有歧义, 且中文同音词极其普遍。在 6.4 万条真实词库 + 30 条
// 正常语料上实测:
//
//	阈值 6  -> 索引 55801 条, 正常文本误报 6/12 (今天->锦天, 小学->小穴), 不可用
//	阈值 8  -> 索引 53161 条, 误报 0/30
//	阈值 10 -> 索引 49881 条, 误报 0/30   <- 采用
//	阈值 12 -> 索引 45775 条, 误报 0/30
//
// 取 10 是在实测安全线 (8) 之上再留一档余量, 仍覆盖 77% 的词条。
// 代价是 "炸药"(zhayao, 6 字符) 这类双字词进不了拼音索引 —— 短拼音的
// 歧义无法通过调参解决, 需要靠语义样本库或人工补拼音词条兜底。
//
// 声明为 var 仅为便于测试扫描阈值, 生产代码不应修改它: 调低等于放开误报。
var MinPinyinLength = 10

// Pinyin 把文本转成无声调拼音。
//
// 非汉字字符原样保留 (小写化): 这样 "zha药" 这种半拼音半汉字的混写
// 也能转成 "zhayao" 与词条拼音对上。表外汉字 (扩展区生僻字) 同样原样保留。
//
// 不插入分隔符: 词条 "炸药" 转成 "zhayao", 输入 "zha yao" 经归一化去空格后
// 也是 "zhayao", 两侧才能直接比较。代价是 "西安" (xian) 与 "先" (xian) 无法
// 区分, 属于拼音匹配的固有歧义, 由 MinPinyinLength 与等级过滤共同兜底。
func Pinyin(text string) string {
	if text == "" {
		return ""
	}
	// 与 AppendPinyinRune 共用同一套规则: 两处若不一致,
	// 逐 rune 建立的位置映射会与整串拼音错位。
	buf := make([]byte, 0, len(text)*2)
	for _, r := range text {
		buf = AppendPinyinRune(buf, r)
	}
	return string(buf)
}

// AppendPinyinRune 把单个 rune 的拼音追加到 dst 并返回结果。
//
// 汉字追加其拼音, 非汉字追加折叠声调后的自身 (小写)。
// 供逐 rune 建立位置映射时使用: 直接查表追加, 避免
// Pinyin(string(r)) 那样为每个字符装箱出两个临时字符串。
func AppendPinyinRune(dst []byte, r rune) []byte {
	if py, ok := hanziPinyin[r]; ok {
		return append(dst, py...)
	}
	folded := foldTone(r)
	if folded >= 'A' && folded <= 'Z' {
		folded += 'a' - 'A'
	}
	return utf8.AppendRune(dst, folded)
}

// PinyinIndexable 判断一个词条是否适合建立拼音索引。
//
// 三条约束, 任一不满足就跳过:
//  1. 拼音长度 >= MinPinyinLength —— 短拼音歧义太大。
//  2. 词条必须含汉字 —— 纯英文/数字词条 (如 "bilibili") 的 "拼音" 就是它自己,
//     建索引没有意义, 只会与原词库重复匹配。
//  3. 拼音必须是纯字母 —— 含数字或符号说明转换不完整, 不可靠。
func PinyinIndexable(word string) (string, bool) {
	if word == "" {
		return "", false
	}
	hasHanzi := false
	for _, r := range word {
		if _, ok := hanziPinyin[r]; ok {
			hasHanzi = true
			break
		}
	}
	if !hasHanzi {
		return "", false
	}

	py := Pinyin(word)
	if len(py) < MinPinyinLength {
		return "", false
	}
	for _, r := range py {
		if r < 'a' || r > 'z' {
			return "", false
		}
	}
	return py, true
}
