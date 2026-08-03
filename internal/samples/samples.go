// Package samples 提供语义样本库: 用整句负面样本补足模型漏报。
//
// 存在意义: 模型权重无法在线更新, 遇到漏报只能等下一轮微调。词库能补单词,
// 但补不了"换几个字的同类句式"。样本库介于两者之间 —— 把漏报的整句存下来,
// 检测时用字符 n-gram 相似度召回, 命中相似句即判定风险。
//
// 与词库的分工:
//   - 词库: 精确匹配已知敏感词, 零误报, 但对句式变体无能为力。
//   - 样本库: 模糊匹配相似句式, 能召回"改写版", 阈值控制误报。
//
// 相似度用 Dice 系数 (基于字符 bigram 集合), 纯 Go 实现, 无外部依赖,
// 单次检测对上千条样本的开销在毫秒级。
package samples

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"noblack/internal/normalize"
)

// DefaultThreshold 是默认的相似度判定阈值。
//
// 取 0.75 的依据: Dice 系数在 0.6 以下会把"同话题但无关"的句子也召回,
// 0.9 以上则只能匹配几乎逐字相同的文本, 失去泛化能力。
// 0.75 附近能容忍替换几个字或调整语序, 同时避免明显误报。
const DefaultThreshold = 0.75

// Sample 是一条负面样本。
type Sample struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`             // 原始文本 (保留供人工核对)
	Levels    []string  `json:"levels"`           // 风险等级, 与词库语义一致
	Remark    string    `json:"remark,omitempty"` // 备注: 为什么加这条
	CreatedAt time.Time `json:"created_at"`

	// bigrams 是归一化文本的字符 bigram 集合, 加载时预计算, 避免每次检测重算。
	bigrams map[string]struct{} `json:"-"`
}

// Match 是一次样本命中。
type Match struct {
	ID         string   `json:"id"`
	Text       string   `json:"text"`
	Levels     []string `json:"levels"`
	Remark     string   `json:"remark,omitempty"`
	Similarity float64  `json:"similarity"`
}

// Store 持有样本集合, 支持并发读与热更新。
type Store struct {
	path      string
	mutex     sync.RWMutex
	samples   []*Sample
	threshold float64
}

// New 创建样本库。path 为空表示不持久化 (仅内存, 重启丢失)。
func New(path string, threshold float64) *Store {
	if threshold <= 0 || threshold > 1 {
		threshold = DefaultThreshold
	}
	return &Store{path: path, threshold: threshold}
}

// Threshold 返回当前相似度阈值。
func (s *Store) Threshold() float64 {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.threshold
}

// Size 返回样本数量。
func (s *Store) Size() int {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return len(s.samples)
}

// bigramsOf 计算文本的字符 bigram 集合 (基于归一化文本, 与检测链路一致)。
//
// 单字文本无法构成 bigram, 退化为该字本身, 保证短样本也可比较。
func bigramsOf(text string) map[string]struct{} {
	folded := normalize.Text(text)
	runes := []rune(folded)
	result := make(map[string]struct{})
	switch {
	case len(runes) == 0:
		return result
	case len(runes) == 1:
		result[string(runes)] = struct{}{}
		return result
	}
	for index := 0; index+1 < len(runes); index++ {
		result[string(runes[index:index+2])] = struct{}{}
	}
	return result
}

// dice 计算两个集合的 Dice 系数: 2|A∩B| / (|A|+|B|), 取值 [0,1]。
func dice(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	// 遍历较小的集合以减少查找次数。
	small, large := a, b
	if len(small) > len(large) {
		small, large = large, small
	}
	intersection := 0
	for gram := range small {
		if _, ok := large[gram]; ok {
			intersection++
		}
	}
	return 2 * float64(intersection) / float64(len(a)+len(b))
}

// FindAll 返回所有相似度达到阈值的样本, 按相似度降序排列。
// 不修改任何状态, 并发安全。
func (s *Store) FindAll(text string) []Match {
	if s == nil {
		return nil
	}
	query := bigramsOf(text)
	if len(query) == 0 {
		return nil
	}

	s.mutex.RLock()
	defer s.mutex.RUnlock()

	var matches []Match
	for _, sample := range s.samples {
		similarity := dice(query, sample.bigrams)
		if similarity < s.threshold {
			continue
		}
		matches = append(matches, Match{
			ID:         sample.ID,
			Text:       sample.Text,
			Levels:     sample.Levels,
			Remark:     sample.Remark,
			Similarity: similarity,
		})
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Similarity != matches[j].Similarity {
			return matches[i].Similarity > matches[j].Similarity
		}
		return matches[i].ID < matches[j].ID
	})
	return matches
}

// List 返回全部样本 (副本), 供管理接口展示。
func (s *Store) List() []Sample {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	out := make([]Sample, 0, len(s.samples))
	for _, sample := range s.samples {
		out = append(out, *sample)
	}
	return out
}

// Add 新增一条样本并持久化。文本重复时返回已有样本, 不重复添加。
func (s *Store) Add(text string, levels []string, remark string) (Sample, bool, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return Sample{}, false, errors.New("样本文本不能为空")
	}
	// 归一化后为空说明整段都是标点/Emoji, 无法用于相似度比较。
	if normalize.Text(trimmed) == "" {
		return Sample{}, false, errors.New("样本文本不含可比较的内容")
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	identifier := idOf(trimmed)
	for _, existing := range s.samples {
		if existing.ID == identifier {
			return *existing, false, nil
		}
	}
	if levels == nil {
		levels = []string{}
	}
	sample := &Sample{
		ID:        identifier,
		Text:      trimmed,
		Levels:    levels,
		Remark:    strings.TrimSpace(remark),
		CreatedAt: time.Now().UTC(),
		bigrams:   bigramsOf(trimmed),
	}
	s.samples = append(s.samples, sample)
	if err := s.persistLocked(); err != nil {
		// 回滚内存状态, 避免落盘失败后内存与磁盘不一致。
		s.samples = s.samples[:len(s.samples)-1]
		return Sample{}, false, err
	}
	return *sample, true, nil
}

// Delete 按 ID 删除样本。返回是否确实删除了。
func (s *Store) Delete(id string) (bool, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for index, sample := range s.samples {
		if sample.ID != id {
			continue
		}
		removed := s.samples[index]
		s.samples = append(s.samples[:index], s.samples[index+1:]...)
		if err := s.persistLocked(); err != nil {
			// 回滚: 把删掉的样本插回原位。
			s.samples = append(s.samples, nil)
			copy(s.samples[index+1:], s.samples[index:])
			s.samples[index] = removed
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// Load 从磁盘读取样本库。文件不存在时视为空库, 不报错。
func (s *Store) Load() error {
	if s.path == "" {
		return nil
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}
	var loaded []*Sample
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return err
	}
	for _, sample := range loaded {
		if sample.Levels == nil {
			sample.Levels = []string{}
		}
		if sample.ID == "" {
			sample.ID = idOf(sample.Text)
		}
		sample.bigrams = bigramsOf(sample.Text)
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.samples = loaded
	return nil
}

// persistLocked 把样本写入磁盘。调用方必须已持有写锁。
// 先写临时文件再原子重命名, 避免写入中途崩溃导致文件损坏。
func (s *Store) persistLocked() error {
	if s.path == "" {
		return nil
	}
	if directory := filepath.Dir(s.path); directory != "" {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return err
		}
	}
	payload, err := json.MarshalIndent(s.samples, "", "  ")
	if err != nil {
		return err
	}
	temporary := s.path + ".tmp"
	if err := os.WriteFile(temporary, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(temporary, s.path)
}

// idOf 由归一化文本派生稳定 ID: 同一句话 (含变体写法) 只会有一个样本。
func idOf(text string) string {
	sum := sha256.Sum256([]byte(normalize.Text(text)))
	return hex.EncodeToString(sum[:8])
}
