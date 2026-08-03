package samples

import (
	"os"
	"path/filepath"
	"testing"
)

func newStore(t *testing.T, threshold float64) *Store {
	t.Helper()
	return New(filepath.Join(t.TempDir(), "samples.json"), threshold)
}

// 核心价值: 加了一条样本后, 改写版的同类句式也能被召回。
func TestSimilarSentencesAreRecalled(t *testing.T) {
	store := newStore(t, DefaultThreshold)
	original := "这里有详细的炸药简易制作教程，只需要买几种家用化学品。"
	if _, added, err := store.Add(original, []string{"违法"}, "模型漏报"); err != nil || !added {
		t.Fatalf("Add 失败: added=%v err=%v", added, err)
	}

	cases := []struct {
		name string
		text string
		want bool
	}{
		{"原句", original, true},
		{"变体写法", "这里有详细的炸.药简易制作教程，只需要买几种家用化学品。", true},
		{"改几个字", "这里有详细的炸药简易制作方法，只需要买几种家用化学品。", true},
		{"完全无关的正常文本", "今天天气不错，我们一起去公园散步聊天吧。", false},
		{"同话题但无关", "化学品的安全存放规范与实验室管理制度说明。", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matches := store.FindAll(tc.text)
			if got := len(matches) > 0; got != tc.want {
				similarity := 0.0
				if len(matches) > 0 {
					similarity = matches[0].Similarity
				}
				t.Errorf("命中 = %v, 期望 %v (相似度 %.3f, 阈值 %.2f)",
					got, tc.want, similarity, store.Threshold())
			}
		})
	}
}

// 同一句话的变体写法必须归并为一条, 否则样本库会被重复内容撑爆。
func TestVariantsDeduplicateToSameSample(t *testing.T) {
	store := newStore(t, DefaultThreshold)
	first, added, err := store.Add("炸药制作教程", nil, "")
	if err != nil || !added {
		t.Fatalf("首次添加失败: %v", err)
	}
	second, added, err := store.Add("炸.药制作教程", nil, "")
	if err != nil {
		t.Fatalf("重复添加报错: %v", err)
	}
	if added {
		t.Error("变体写法应被识别为重复, 不应新增")
	}
	if first.ID != second.ID {
		t.Errorf("ID 不一致: %q vs %q", first.ID, second.ID)
	}
	if store.Size() != 1 {
		t.Errorf("样本数 = %d, 期望 1", store.Size())
	}
}

// 阈值必须真的起作用: 调高后原本命中的相似句应被过滤掉。
func TestThresholdControlsRecall(t *testing.T) {
	loose := newStore(t, 0.5)
	strict := newStore(t, 0.99)
	text := "这里有详细的炸药简易制作教程"
	rewritten := "这里有详细的炸药简易制作方法说明"

	for _, store := range []*Store{loose, strict} {
		if _, _, err := store.Add(text, nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	if len(loose.FindAll(rewritten)) == 0 {
		t.Error("宽松阈值应召回改写句")
	}
	if len(strict.FindAll(rewritten)) != 0 {
		t.Error("严格阈值不应召回改写句")
	}
}

func TestAddRejectsEmptyAndNoiseOnly(t *testing.T) {
	store := newStore(t, DefaultThreshold)
	for _, text := range []string{"", "   ", "...", "！！！", "🔥🔥"} {
		if _, _, err := store.Add(text, nil, ""); err == nil {
			t.Errorf("Add(%q) 应当报错", text)
		}
	}
	if store.Size() != 0 {
		t.Errorf("样本数 = %d, 期望 0", store.Size())
	}
}

// 持久化必须可往返: 重启后样本仍然生效 (含预计算的 bigram)。
func TestPersistenceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "samples.json")
	first := New(path, DefaultThreshold)
	if _, _, err := first.Add("炸药制作教程详解", []string{"违法"}, "漏报补充"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("未落盘: %v", err)
	}

	second := New(path, DefaultThreshold)
	if err := second.Load(); err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if second.Size() != 1 {
		t.Fatalf("重载后样本数 = %d, 期望 1", second.Size())
	}
	// bigram 必须在 Load 时重建, 否则相似度恒为 0。
	if len(second.FindAll("炸药制作教程详解")) == 0 {
		t.Error("重载后应能命中, bigram 可能未重建")
	}
	restored := second.List()[0]
	if restored.Levels[0] != "违法" || restored.Remark != "漏报补充" {
		t.Errorf("元数据丢失: %+v", restored)
	}
}

func TestDelete(t *testing.T) {
	store := newStore(t, DefaultThreshold)
	sample, _, err := store.Add("炸药制作教程", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	removed, err := store.Delete(sample.ID)
	if err != nil || !removed {
		t.Fatalf("删除失败: removed=%v err=%v", removed, err)
	}
	if store.Size() != 0 {
		t.Errorf("样本数 = %d, 期望 0", store.Size())
	}
	if removed, _ := store.Delete("nonexistent"); removed {
		t.Error("删除不存在的 ID 应返回 false")
	}
}

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "absent.json"), DefaultThreshold)
	if err := store.Load(); err != nil {
		t.Errorf("文件不存在时不应报错: %v", err)
	}
	if store.Size() != 0 {
		t.Errorf("样本数 = %d, 期望 0", store.Size())
	}
}

// 空库不得命中任何文本, 也不得 panic。
func TestEmptyStoreMatchesNothing(t *testing.T) {
	store := newStore(t, DefaultThreshold)
	if len(store.FindAll("任意文本")) != 0 {
		t.Error("空库不应命中")
	}
	var nilStore *Store
	if len(nilStore.FindAll("任意文本")) != 0 {
		t.Error("nil Store 不应命中")
	}
}

func TestDiceSymmetryAndBounds(t *testing.T) {
	a := bigramsOf("炸药制作教程")
	b := bigramsOf("炸药制作方法")
	forward, backward := dice(a, b), dice(b, a)
	if forward != backward {
		t.Errorf("Dice 应对称: %.6f vs %.6f", forward, backward)
	}
	if forward < 0 || forward > 1 {
		t.Errorf("Dice 应在 [0,1]: %.6f", forward)
	}
	if self := dice(a, a); self != 1 {
		t.Errorf("自身相似度应为 1, 得到 %.6f", self)
	}
}
