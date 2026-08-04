package samples

import (
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return New(filepath.Join(t.TempDir(), "s.json"), DefaultThreshold)
}

// 等级留空时应回落到 Low, 而不是留空数组 —— 空等级会让下游无法按风险分级。
func TestAddDefaultsLevel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		levels []string
	}{
		{"nil", nil},
		{"空数组", []string{}},
		{"全是空白", []string{"", "  "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			s, _, err := st.Add("这是一条用于测试默认等级的样本文本", tc.levels, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(s.Levels) != 1 || s.Levels[0] != DefaultLevel {
				t.Errorf("levels=%v, 期望 [%s]", s.Levels, DefaultLevel)
			}
		})
	}
}

func TestAddKeepsExplicitLevels(t *testing.T) {
	st := newTestStore(t)
	s, _, err := st.Add("显式指定等级的样本文本内容", []string{"High", "色情"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Levels) != 2 || s.Levels[0] != "High" {
		t.Errorf("levels=%v, 应保留显式值", s.Levels)
	}
}

// 只改等级与备注, 不动文本时 ID 应保持不变。
func TestUpdateKeepsIDWhenTextUnchanged(t *testing.T) {
	st := newTestStore(t)
	original, _, err := st.Add("原始样本文本内容用于测试修改", []string{"Low"}, "旧备注")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := st.Update(original.ID, "", []string{"High"}, "新备注")
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != original.ID {
		t.Errorf("ID 变了: %s -> %s, 未改文本时应保持", original.ID, updated.ID)
	}
	if updated.Text != original.Text {
		t.Errorf("文本被改动: %q", updated.Text)
	}
	if len(updated.Levels) != 1 || updated.Levels[0] != "High" {
		t.Errorf("levels=%v, 期望 [High]", updated.Levels)
	}
	if updated.Remark != "新备注" {
		t.Errorf("remark=%q", updated.Remark)
	}
}

// ID 由文本哈希生成, 改文本必然换 ID —— 这是有意的, 调用方据此刷新列表。
func TestUpdateChangesIDWhenTextChanged(t *testing.T) {
	st := newTestStore(t)
	original, _, err := st.Add("原始文本内容用于测试标识变化", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := st.Update(original.ID, "修改后的全新文本内容用于测试", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID == original.ID {
		t.Error("改了文本, ID 应随之变化")
	}
	if st.Size() != 1 {
		t.Errorf("样本数=%d, 修改不应产生新条目", st.Size())
	}
	// 旧 ID 不应再能定位到样本
	if _, err := st.Update(original.ID, "", nil, ""); !IsNotFound(err) {
		t.Errorf("旧 ID 仍可定位, err=%v", err)
	}
}

// 改成与另一条重复的文本应报错, 而不是静默合并掉一条。
func TestUpdateRejectsDuplicateText(t *testing.T) {
	st := newTestStore(t)
	first, _, _ := st.Add("第一条样本的文本内容", nil, "")
	second, _, _ := st.Add("第二条样本的文本内容", nil, "")

	_, err := st.Update(second.ID, first.Text, nil, "")
	if err == nil {
		t.Fatal("改成重复文本应报错")
	}
	if IsNotFound(err) {
		t.Errorf("错误类型不对: %v", err)
	}
	if st.Size() != 2 {
		t.Errorf("样本数=%d, 失败的修改不应影响数量", st.Size())
	}
}

func TestUpdateNotFound(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.Update("nonexistent", "", nil, ""); !IsNotFound(err) {
		t.Errorf("应返回 not found, 实际 %v", err)
	}
}

// 修改后应能持久化并被重新加载。
func TestUpdatePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	st := New(path, DefaultThreshold)
	original, _, _ := st.Add("需要持久化验证的样本文本", nil, "")
	if _, err := st.Update(original.ID, "", []string{"High"}, "已修改"); err != nil {
		t.Fatal(err)
	}

	reloaded := New(path, DefaultThreshold)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	list := reloaded.List()
	if len(list) != 1 {
		t.Fatalf("重载后样本数=%d", len(list))
	}
	if len(list[0].Levels) != 1 || list[0].Levels[0] != "High" {
		t.Errorf("重载后 levels=%v", list[0].Levels)
	}
	if list[0].Remark != "已修改" {
		t.Errorf("重载后 remark=%q", list[0].Remark)
	}
}
