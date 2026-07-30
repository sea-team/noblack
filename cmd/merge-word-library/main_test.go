package main

import (
	"reflect"
	"testing"
)

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
