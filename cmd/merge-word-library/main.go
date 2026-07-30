package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"noblack/internal/matcher"
)

var categoryNames = map[string]string{
	"0": "政治",
	"1": "毒品",
	"2": "色情",
	"3": "赌博",
	"4": "违法",
}

type sourceEntry struct {
	word    string
	levels  map[string]struct{}
	remarks map[string]struct{}
}

type wordFile struct {
	Words []matcher.Entry `json:"words"`
}

func main() {
	dictPath := flag.String("dict", "", "sensitive-word-go 黑名单词库")
	tagsPath := flag.String("tags", "", "sensitive-word-go 标签词库")
	allowPath := flag.String("allow", "", "sensitive-word-go 白名单词库")
	denyPath := flag.String("deny", "", "sensitive-word-go 用户黑名单词库")
	basePath := flag.String("base", "", "已有 Noblack JSON 词库, 可为空")
	outputPath := flag.String("output", "./data/words.json", "输出 Noblack JSON 词库")
	flag.Parse()

	if *dictPath == "" || *tagsPath == "" {
		fatalf("-dict 和 -tags 不能为空")
	}

	dict, err := readLines(*dictPath)
	if err != nil {
		fatalf("读取 dict 失败: %v", err)
	}
	tags, err := readLines(*tagsPath)
	if err != nil {
		fatalf("读取 tags 失败: %v", err)
	}
	allow, err := readOptionalLines(*allowPath)
	if err != nil {
		fatalf("读取 allow 失败: %v", err)
	}
	deny, err := readOptionalLines(*denyPath)
	if err != nil {
		fatalf("读取 deny 失败: %v", err)
	}

	entries := mergeWordSources(dict, tags, allow, deny)
	if *basePath != "" {
		base, err := readEntries(*basePath)
		if err != nil {
			fatalf("读取已有词库失败: %v", err)
		}
		entries = mergeEntries(base, entries, allowSet(allow))
	}
	if err := writeEntries(*outputPath, entries); err != nil {
		fatalf("写入词库失败: %v", err)
	}
	fmt.Printf("merged %d entries into %s\n", len(entries), *outputPath)
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func parseSourceLine(line string) (string, []string, bool) {
	line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
	if line == "" || strings.HasPrefix(line, "#") {
		return "", nil, false
	}
	separator := strings.LastIndexAny(line, " \t")
	if separator < 0 {
		return line, nil, true
	}
	word := strings.TrimSpace(line[:separator])
	valueText := strings.TrimSpace(line[separator:])
	if word == "" || valueText == "" {
		return line, nil, true
	}
	return word, splitValues(valueText), true
}

func splitValues(valueText string) []string {
	parts := strings.FieldsFunc(valueText, func(r rune) bool {
		return r == ',' || r == '，' || unicode.IsSpace(r)
	})
	values := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, exists := seen[part]; exists {
			continue
		}
		seen[part] = struct{}{}
		values = append(values, part)
	}
	return values
}

func splitWords(wordText string) []string {
	parts := strings.FieldsFunc(wordText, func(r rune) bool {
		return r == ',' || r == '，'
	})
	words := make([]string, 0, len(parts))
	for _, word := range parts {
		word = normalizeWord(word)
		if word != "" {
			words = append(words, word)
		}
	}
	return words
}

func normalizeWord(word string) string {
	word = strings.TrimSpace(strings.TrimPrefix(word, "\ufeff"))
	return strings.Map(func(r rune) rune {
		return unicode.ToLower(r)
	}, word)
}

func mergeWordSources(dictLines, tagLines, allowLines, denyLines []string) []matcher.Entry {
	allow := allowSet(allowLines)
	merged := make(map[string]*sourceEntry)
	addLines(merged, dictLines, allow, true, false)
	addLines(merged, tagLines, allow, true, true)
	addLines(merged, denyLines, allow, true, false)
	return sourceEntriesToMatcherEntries(merged)
}

func addLines(merged map[string]*sourceEntry, lines []string, allow map[string]struct{}, categoryAsLevel, valueAsRemark bool) {
	for _, line := range lines {
		wordText, values, ok := parseSourceLine(line)
		if !ok {
			continue
		}
		for _, word := range splitWords(wordText) {
			key := normalizeWord(word)
			if _, allowed := allow[key]; allowed {
				continue
			}
			entry := merged[key]
			if entry == nil {
				entry = &sourceEntry{word: word, levels: map[string]struct{}{}, remarks: map[string]struct{}{}}
				merged[key] = entry
			}
			for _, value := range values {
				if category, exists := categoryNames[value]; exists {
					value = category
				}
				if categoryAsLevel {
					entry.levels[value] = struct{}{}
				}
				if valueAsRemark {
					entry.remarks[value] = struct{}{}
				}
			}
		}
	}
}

func allowSet(lines []string) map[string]struct{} {
	allowed := make(map[string]struct{})
	for _, line := range lines {
		wordText, _, ok := parseSourceLine(line)
		if !ok {
			continue
		}
		for _, word := range splitWords(wordText) {
			allowed[normalizeWord(word)] = struct{}{}
		}
	}
	return allowed
}

func mergeEntries(base, imported []matcher.Entry, allow map[string]struct{}) []matcher.Entry {
	merged := make(map[string]*sourceEntry)
	addEntries(merged, base, allow)
	addEntries(merged, imported, allow)
	return sourceEntriesToMatcherEntries(merged)
}

func addEntries(merged map[string]*sourceEntry, entries []matcher.Entry, allow map[string]struct{}) {
	for _, item := range entries {
		for _, word := range splitWords(item.Word) {
			key := normalizeWord(word)
			if _, allowed := allow[key]; allowed {
				continue
			}
			entry := merged[key]
			if entry == nil {
				entry = &sourceEntry{word: word, levels: map[string]struct{}{}, remarks: map[string]struct{}{}}
				merged[key] = entry
			}
			for _, level := range item.Levels {
				if level = strings.TrimSpace(level); level != "" {
					entry.levels[level] = struct{}{}
				}
			}
			for _, remark := range item.Remarks {
				if remark = strings.TrimSpace(remark); remark != "" {
					entry.remarks[remark] = struct{}{}
				}
			}
		}
	}
}

func sourceEntriesToMatcherEntries(merged map[string]*sourceEntry) []matcher.Entry {
	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	entries := make([]matcher.Entry, 0, len(keys))
	for _, key := range keys {
		item := merged[key]
		levels := sortedSet(item.levels)
		if len(levels) == 0 {
			levels = []string{"Low"}
		}
		entries = append(entries, matcher.Entry{
			Word:    item.word,
			Levels:  levels,
			Remarks: sortedSet(item.remarks),
		})
	}
	return entries
}

func sortedSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func readLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	lines := make([]string, 0)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

func readOptionalLines(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	return readLines(path)
}

func readEntries(path string) ([]matcher.Entry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file wordFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	return file.Words, nil
}

func writeEntries(path string, entries []matcher.Entry) error {
	data, err := json.MarshalIndent(wordFile{Words: entries}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o644); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
