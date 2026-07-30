package main

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMergeCommandWritesDataWordDatabaseByDefault(t *testing.T) {
	directory := t.TempDir()
	dictPath := filepath.Join(directory, "dict.txt")
	tagsPath := filepath.Join(directory, "tags.txt")
	if err := os.WriteFile(dictPath, []byte("测试词 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tagsPath, []byte("测试词 色情\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(os.Args[0], "-test.run=TestMergeDefaultOutputHelper")
	command.Dir = directory
	command.Env = append(
		os.Environ(),
		"NOBLACK_MERGE_DEFAULT_OUTPUT_HELPER=1",
		"NOBLACK_MERGE_DICT="+dictPath,
		"NOBLACK_MERGE_TAGS="+tagsPath,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("merge command failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(directory, "data", "words.json")); err != nil {
		t.Fatalf("data/words.json was not generated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "words.json")); !os.IsNotExist(err) {
		t.Fatalf("root words.json unexpectedly exists: %v", err)
	}
}

func TestMergeDefaultOutputHelper(t *testing.T) {
	if os.Getenv("NOBLACK_MERGE_DEFAULT_OUTPUT_HELPER") != "1" {
		return
	}
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	os.Args = []string{
		os.Args[0],
		"-dict", os.Getenv("NOBLACK_MERGE_DICT"),
		"-tags", os.Getenv("NOBLACK_MERGE_TAGS"),
	}
	main()
}

func TestParseSourceLineSplitsWordAndValues(t *testing.T) {
	word, values, ok := parseSourceLine("  测试词  1,4  ")
	if !ok {
		t.Fatal("expected source line to parse")
	}
	if word != "测试词" {
		t.Fatalf("word = %q, want 测试词", word)
	}
	if !reflect.DeepEqual(values, []string{"1", "4"}) {
		t.Fatalf("values = %#v, want [1 4]", values)
	}
}

func TestMergeWordSourcesCombinesCategoriesAndTags(t *testing.T) {
	entries := mergeWordSources(
		[]string{"测试词 1,4"},
		[]string{"测试词 色情,违法"},
		nil,
		nil,
	)
	if len(entries) != 1 {
		t.Fatalf("entries = %#v, want one merged entry", entries)
	}
	if entries[0].Word != "测试词" {
		t.Fatalf("word = %q", entries[0].Word)
	}
	if !reflect.DeepEqual(entries[0].Levels, []string{"毒品", "色情", "违法"}) {
		t.Fatalf("levels = %#v, want [毒品 色情 违法]", entries[0].Levels)
	}
	if !reflect.DeepEqual(entries[0].Remarks, []string{"色情", "违法"}) {
		t.Fatalf("remarks = %#v, want [色情 违法]", entries[0].Remarks)
	}
}

func TestMergeWordSourcesAllowListWinsAndDenyListIsRetained(t *testing.T) {
	entries := mergeWordSources(
		[]string{"允许词 2", "拒绝词 4"},
		nil,
		[]string{"允许词"},
		[]string{"拒绝词"},
	)
	if len(entries) != 1 {
		t.Fatalf("entries = %#v, want allow-list word removed", entries)
	}
	words := map[string]bool{}
	for _, entry := range entries {
		words[entry.Word] = true
	}
	if words["允许词"] || !words["拒绝词"] {
		t.Fatalf("entries = %#v, want only deny-list word", entries)
	}
}

func TestMergeWordSourcesDeduplicatesCommaWords(t *testing.T) {
	entries := mergeWordSources(
		[]string{"甲,乙 2", "乙 2"},
		nil,
		nil,
		nil,
	)
	if len(entries) != 2 {
		t.Fatalf("entries = %#v, want two unique entries", entries)
	}
}
