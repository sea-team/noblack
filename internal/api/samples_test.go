package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"noblack/internal/matcher"
	"noblack/internal/samples"
	"noblack/internal/stats"
	"noblack/internal/store"
)

func newSampleHandler(t *testing.T, token string) *Handler {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/words.json"
	entries := []matcher.Entry{{Word: "挖矿", Levels: []string{"L"}}}
	if err := matcher.SaveEntries(path, entries); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(store.New(path, entries, matcher.Options{Normalize: true}), stats.New(), token)
	h.SetSampleStore(samples.New(filepath.Join(dir, "samples.json"), samples.DefaultThreshold))
	h.SetDetectMode(ModeWordOnly, false)
	return h
}

// 核心场景: 词库拦不住的句子, 提交为样本后立即生效。
func TestSampleSubmissionTakesEffectImmediately(t *testing.T) {
	h := newSampleHandler(t, "")
	const text = "搞点那种一次性的东西，材料网上都能买到，我教你怎么弄。"

	before := decodeCheck(t, do(h, http.MethodPost, "/check", `{"text":`+quote(text)+`}`, nil))
	if before.Blocked {
		t.Fatal("前置条件不成立: 该句本应未被拦截")
	}

	rec := do(h, http.MethodPost, "/samples",
		`{"text":`+quote(text)+`,"levels":["违法"],"remark":"模型漏报"}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("提交样本状态码 = %d, 期望 201; body=%s", rec.Code, rec.Body.String())
	}

	after := decodeCheck(t, do(h, http.MethodPost, "/check", `{"text":`+quote(text)+`}`, nil))
	if !after.Blocked {
		t.Error("提交样本后应当拦截")
	}
	if after.DecidedBy != decidedBySample {
		t.Errorf("decided_by = %q, 期望 %q", after.DecidedBy, decidedBySample)
	}
	if len(after.SampleMatches) == 0 {
		t.Fatal("应返回命中的样本")
	}
	if after.SampleMatches[0].Similarity < 0.99 {
		t.Errorf("原句相似度 = %.3f, 期望接近 1", after.SampleMatches[0].Similarity)
	}
}

// 样本库要能召回改写版, 这是它相对词库的核心价值。
func TestSampleRecallsRewrittenSentence(t *testing.T) {
	h := newSampleHandler(t, "")
	original := "搞点那种一次性的东西，材料网上都能买到，我教你怎么弄。"
	rewritten := "搞点那种一次性的东西，材料网上都能买到，我教你怎么做。"

	do(h, http.MethodPost, "/samples", `{"text":`+quote(original)+`}`, nil)

	data := decodeCheck(t, do(h, http.MethodPost, "/check", `{"text":`+quote(rewritten)+`}`, nil))
	if !data.Blocked {
		t.Error("改写版应被召回")
	}
}

// 不相关的正常文本不得被样本库误拦。
func TestSampleDoesNotBlockUnrelatedText(t *testing.T) {
	h := newSampleHandler(t, "")
	do(h, http.MethodPost, "/samples",
		`{"text":"搞点那种一次性的东西，材料网上都能买到，我教你怎么弄。"}`, nil)

	for _, text := range []string{
		"今天天气不错，我们一起去公园散步聊天。",
		"这个东西网上都能买到，我教你怎么用。",
		"材料科学是一门很有意思的学科。",
	} {
		data := decodeCheck(t, do(h, http.MethodPost, "/check", `{"text":`+quote(text)+`}`, nil))
		if data.Blocked {
			t.Errorf("正常文本被误拦: %q (相似度 %+v)", text, data.SampleMatches)
		}
	}
}

// 词库已命中时不应被样本库改写归因 —— 更精确的结论优先。
func TestSampleDoesNotOverrideWordDecision(t *testing.T) {
	h := newSampleHandler(t, "")
	do(h, http.MethodPost, "/samples", `{"text":"挖矿相关的样本文本内容测试"}`, nil)

	data := decodeCheck(t, do(h, http.MethodPost, "/check", `{"text":"挖矿相关的样本文本内容测试"}`, nil))
	if !data.Blocked {
		t.Fatal("应当拦截")
	}
	if data.DecidedBy != decidedByWords {
		t.Errorf("decided_by = %q, 词库命中时应归因 words", data.DecidedBy)
	}
}

func TestSampleWritesRequireToken(t *testing.T) {
	h := newSampleHandler(t, "s3cret")

	if rec := do(h, http.MethodPost, "/samples", `{"text":"需要令牌的样本文本"}`, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("无令牌 POST 状态码 = %d, 期望 401", rec.Code)
	}
	if rec := do(h, http.MethodDelete, "/samples/abc", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("无令牌 DELETE 状态码 = %d, 期望 401", rec.Code)
	}
	// 读操作不需要令牌。
	if rec := do(h, http.MethodGet, "/samples", "", nil); rec.Code != http.StatusOK {
		t.Errorf("GET 状态码 = %d, 读操作应开放", rec.Code)
	}
	// 带令牌可写。
	headers := map[string]string{"X-Auth-Token": "s3cret"}
	if rec := do(h, http.MethodPost, "/samples", `{"text":"带令牌的样本文本"}`, headers); rec.Code != http.StatusCreated {
		t.Errorf("带令牌 POST 状态码 = %d, 期望 201", rec.Code)
	}
}

// 重复提交返回 200 而非 201, 并标明 added=false, 便于调用方区分。
func TestDuplicateSampleReportsNotAdded(t *testing.T) {
	h := newSampleHandler(t, "")
	const body = `{"text":"重复提交检测用的样本文本"}`

	if rec := do(h, http.MethodPost, "/samples", body, nil); rec.Code != http.StatusCreated {
		t.Fatalf("首次提交状态码 = %d, 期望 201", rec.Code)
	}
	rec := do(h, http.MethodPost, "/samples", body, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("重复提交状态码 = %d, 期望 200", rec.Code)
	}
	var resp struct {
		Data struct {
			Added bool `json:"added"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.Added {
		t.Error("重复提交应返回 added=false")
	}
}

func TestSampleDeleteFlow(t *testing.T) {
	h := newSampleHandler(t, "")
	rec := do(h, http.MethodPost, "/samples", `{"text":"待删除的样本文本内容"}`, nil)
	var created struct {
		Data struct {
			Sample samples.Sample `json:"sample"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	if rec := do(h, http.MethodDelete, "/samples/"+created.Data.Sample.ID, "", nil); rec.Code != http.StatusOK {
		t.Errorf("删除状态码 = %d, 期望 200", rec.Code)
	}
	if rec := do(h, http.MethodDelete, "/samples/"+created.Data.Sample.ID, "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("重复删除状态码 = %d, 期望 404", rec.Code)
	}
}

// 未启用样本库时接口应明确报错, 而非静默返回空结果。
func TestSampleEndpointsDisabledWithoutStore(t *testing.T) {
	h := newTestHandler(t, "")
	for _, call := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/samples"},
		{http.MethodPost, "/samples"},
		{http.MethodDelete, "/samples/abc"},
	} {
		rec := do(h, call.method, call.path, `{"text":"x"}`, nil)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s 状态码 = %d, 期望 503", call.method, call.path, rec.Code)
		}
	}
}

func TestSampleRejectsEmptyText(t *testing.T) {
	h := newSampleHandler(t, "")
	for _, body := range []string{`{"text":""}`, `{"text":"   "}`, `{"text":"..."}`} {
		if rec := do(h, http.MethodPost, "/samples", body, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("body=%s 状态码 = %d, 期望 400", body, rec.Code)
		}
	}
}
