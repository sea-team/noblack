package store

import (
	"os"
	"path/filepath"
	"testing"

	"noblack/internal/matcher"
)

// newTempStore 在临时目录建一个词库文件并返回 Store。
func newTempStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "words.json")
	entries := []matcher.Entry{
		{Word: "挖矿", Levels: []string{"bilibili"}, Remarks: []string{"引流站点"}},
	}
	if err := matcher.SaveEntries(path, entries); err != nil {
		t.Fatal(err)
	}
	loaded, err := matcher.LoadEntries(path, matcher.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return New(path, loaded, matcher.Options{})
}

func TestListEntriesPageSortsFiltersAndPaginates(t *testing.T) {
	s := New("", []matcher.Entry{
		{Word: "delta", Levels: []string{"Normal"}, Remarks: []string{"fourth"}},
		{Word: "Alpha", Levels: []string{"Safe"}, Remarks: []string{"first"}},
		{Word: "charlie", Levels: []string{"Gambling"}, Remarks: []string{"third"}},
		{Word: "bravo", Levels: []string{"Review"}, Remarks: []string{"SECOND"}},
	}, matcher.Options{})

	page := s.ListEntriesPage(2, 2, "")
	if page.Total != 4 || len(page.Entries) != 2 {
		t.Fatalf("page = %+v, want total 4 and 2 entries", page)
	}
	if page.Entries[0].Word != "charlie" || page.Entries[1].Word != "delta" {
		t.Fatalf("page entries = %+v, want [charlie delta]", page.Entries)
	}

	cases := []struct {
		query string
		want  string
	}{
		{query: "ALP", want: "Alpha"},
		{query: "gambling", want: "charlie"},
		{query: "second", want: "bravo"},
	}
	for _, tc := range cases {
		result := s.ListEntriesPage(1, 10, tc.query)
		if result.Total != 1 || len(result.Entries) != 1 || result.Entries[0].Word != tc.want {
			t.Fatalf("query %q result = %+v, want one entry %q", tc.query, result, tc.want)
		}
	}
	for _, tc := range []struct {
		mode string
		want string
	}{
		{mode: "exact", want: "Alpha"},
		{mode: "prefix", want: "Alpha"},
		{mode: "suffix", want: "bravo"},
	} {
		query := map[string]string{"exact": "alpha", "prefix": "alp", "suffix": "avo"}[tc.mode]
		result := s.ListEntriesPageMatch(1, 10, query, tc.mode)
		if result.Total != 1 || result.Entries[0].Word != tc.want {
			t.Fatalf("mode %q result = %+v, want %q", tc.mode, result, tc.want)
		}
	}

	beyond := s.ListEntriesPage(3, 2, "")
	if beyond.Total != 4 || beyond.Entries == nil || len(beyond.Entries) != 0 {
		t.Fatalf("beyond page = %+v, want total 4 and empty non-nil entries", beyond)
	}
}

func TestAddUpdateDelete(t *testing.T) {
	s := newTempStore(t)

	// 新增。
	if err := s.AddEntry(matcher.Entry{Word: "大雷", Levels: []string{"High"}, Remarks: []string{"a", "b"}}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// 重复新增应报错。
	if err := s.AddEntry(matcher.Entry{Word: "大雷", Levels: []string{"High"}}); err == nil {
		t.Error("重复新增应报错")
	}
	// 命中新词。
	if m := s.Current().FindAll("你是大雷"); len(m) == 0 || m[0].Word != "大雷" {
		t.Errorf("新增后未命中: %+v", m)
	}

	// 更新。
	if err := s.UpdateEntry(matcher.Entry{Word: "大雷", Levels: []string{"Low"}, Remarks: nil}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if m := s.Current().FindAll("大雷"); len(m) == 0 || m[0].Levels[0] != "Low" {
		t.Errorf("更新等级未生效: %+v", m)
	}
	// 更新不存在的词报错。
	if err := s.UpdateEntry(matcher.Entry{Word: "不存在", Levels: []string{"x"}}); err == nil {
		t.Error("更新不存在词应报错")
	}

	// 删除。
	if err := s.DeleteEntry("大雷"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if m := s.Current().FindAll("大雷"); len(m) != 0 {
		t.Errorf("删除后仍命中: %+v", m)
	}
	if err := s.DeleteEntry("大雷"); err == nil {
		t.Error("删除不存在词应报错")
	}
}

// 增删改后, 磁盘文件应同步更新 (可被重新加载验证)。
func TestPersistence(t *testing.T) {
	s := newTempStore(t)
	if err := s.AddEntry(matcher.Entry{Word: "六合彩", Levels: []string{"赌博", "诈骗"}}); err != nil {
		t.Fatal(err)
	}

	// 直接从磁盘重新读, 应包含新词。
	data, _ := os.ReadFile(s.Path())
	entries, err := matcher.LoadEntries(s.Path(), matcher.Options{})
	if err != nil {
		t.Fatalf("重载失败: %v\n文件内容:\n%s", err, data)
	}
	found := false
	for _, e := range entries {
		if e.Word == "六合彩" && len(e.Levels) == 2 {
			found = true
		}
	}
	if !found {
		t.Errorf("新增词未持久化到磁盘, 文件:\n%s", data)
	}
}

func TestUpsert(t *testing.T) {
	s := newTempStore(t)
	created, err := s.UpsertEntry(matcher.Entry{Word: "新词", Levels: []string{"A"}})
	if err != nil || !created {
		t.Errorf("Upsert 新增: created=%v err=%v", created, err)
	}
	created, err = s.UpsertEntry(matcher.Entry{Word: "新词", Levels: []string{"B"}})
	if err != nil || created {
		t.Errorf("Upsert 更新: created=%v err=%v", created, err)
	}
	list := s.ListEntries()
	for _, e := range list {
		if e.Word == "新词" && e.Levels[0] != "B" {
			t.Errorf("Upsert 未更新等级: %+v", e)
		}
	}
}

func TestRejectExpandedDuplicateOnAdd(t *testing.T) {
	s := newTempStore(t)
	if err := s.AddEntry(matcher.Entry{Word: "挖矿,其他词", Levels: []string{"A"}}); err == nil {
		t.Fatal("预期拒绝展开后重复的词条")
	}
	if got := s.ListEntries(); len(got) != 1 || got[0].Word != "挖矿" {
		t.Fatalf("拒绝重复词条后 Store 发生了变化: %+v", got)
	}
}

func TestAddOrMergeEntryExpandsCompatibleBatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "words.json")
	entries := []matcher.Entry{{Word: "mother-a,mother-b,mother-c", Levels: []string{"Medium"}, Remarks: []string{"abuse"}}}
	if err := matcher.SaveEntries(path, entries); err != nil {
		t.Fatal(err)
	}
	s := New(path, entries, matcher.Options{})
	result, err := s.AddOrMergeEntry(matcher.Entry{
		Word: "mother-a,mother-b,mother-c,mother-d", Levels: []string{"Medium"}, Remarks: []string{"abuse"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Merged || result.Created {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(result.AddedWords) != 1 || result.AddedWords[0] != "mother-d" || len(result.ReusedWords) != 3 {
		t.Fatalf("unexpected word changes: %+v", result)
	}
	list := s.ListEntries()
	if len(list) != 1 || list[0].Word != "mother-a,mother-b,mother-c,mother-d" {
		t.Fatalf("unexpected merged entries: %+v", list)
	}
	loaded, err := matcher.LoadEntries(path, matcher.Options{})
	if err != nil || len(loaded) != 1 || loaded[0].Word != list[0].Word {
		t.Fatalf("merged entry not persisted: entries=%+v err=%v", loaded, err)
	}
	if matches := s.Current().FindAll("prefix mother-d suffix"); len(matches) != 1 || matches[0].Word != "mother-d" {
		t.Fatalf("new word is not active: %+v", matches)
	}
}

func TestAddOrMergeEntryRejectsMetadataConflict(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "words.json")
	entries := []matcher.Entry{{Word: "existing", Levels: []string{"A"}, Remarks: []string{"old"}}}
	if err := matcher.SaveEntries(path, entries); err != nil {
		t.Fatal(err)
	}
	s := New(path, entries, matcher.Options{})
	_, err := s.AddOrMergeEntry(matcher.Entry{Word: "existing,new-word", Levels: []string{"Other"}})
	if err == nil {
		t.Fatal("expected metadata conflict")
	}
	if got := s.ListEntries(); len(got) != 1 || got[0].Word != "existing" {
		t.Fatalf("store changed after conflict: %+v", got)
	}
}

func TestAddOrMergeEntryIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "words.json")
	entries := []matcher.Entry{{Word: "existing", Levels: []string{"A"}, Remarks: []string{"old"}}}
	if err := matcher.SaveEntries(path, entries); err != nil {
		t.Fatal(err)
	}
	s := New(path, entries, matcher.Options{})
	result, err := s.AddOrMergeEntry(matcher.Entry{Word: "existing", Levels: []string{"A"}, Remarks: []string{"old"}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Merged || len(result.AddedWords) != 0 || len(result.ReusedWords) != 1 {
		t.Fatalf("unexpected idempotent result: %+v", result)
	}
	if got := s.ListEntries(); len(got) != 1 {
		t.Fatalf("idempotent merge duplicated entries: %+v", got)
	}
}
