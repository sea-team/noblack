package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"noblack/internal/matcher"
	"noblack/internal/modelclient"
	"noblack/internal/stats"
	"noblack/internal/store"
)

// stubModel 启动一个假的模型服务。
//   - action 为返回的 combined_action ("pass" 表示未命中)。
//   - fail 为 true 时返回 500, 模拟技术失败 (服务不可用)。
//
// 返回的 calls 指针记录实际调用次数, 用于验证短路是否真的省掉了调用。
func stubModel(t *testing.T, action string, fail bool) (*modelclient.Client, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/predict" {
			calls++
			if fail {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok":              true,
			"device":          "cpu",
			"parallel":        true,
			"combined_action": action,
			"combine_policy":  "max",
			"models": []map[string]any{
				{"model": "lite", "action": action},
				{"model": "macbert", "action": action},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return modelclient.New(srv.URL, 5*time.Second), &calls
}

// newDetectHandler 构造一个词库只含 "挖矿" 的 Handler。
func newDetectHandler(t *testing.T) *Handler {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/words.json"
	entries := []matcher.Entry{{Word: "挖矿", Levels: []string{"L"}}}
	if err := matcher.SaveEntries(path, entries); err != nil {
		t.Fatal(err)
	}
	return NewHandler(store.New(path, entries, matcher.Options{}), stats.New(), "")
}

func decodeCheck(t *testing.T, rec *httptest.ResponseRecorder) checkData {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, 期望 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data checkData `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v; body=%s", err, rec.Body.String())
	}
	return resp.Data
}

// 核心矩阵: 五种模式 × 词库命中/未命中 × 模型命中/未命中/技术失败。
func TestDetectModes(t *testing.T) {
	const hitText = `{"text":"挖矿教程"}`    // 词库命中
	const missText = `{"text":"今天天气不错"}` // 词库未命中

	cases := []struct {
		name        string
		mode        DetectMode
		body        string
		modelAction string
		modelFails  bool
		wantBlocked bool
		wantDecider string
		wantCalls   int // 期望的模型调用次数
	}{
		// --- 仅词库: 完全不碰模型 ---
		{"仅词库_命中", ModeWordOnly, hitText, "block", false, true, decidedByWords, 0},
		{"仅词库_未命中", ModeWordOnly, missText, "block", false, false, decidedByNone, 0},

		// --- 仅模型: 技术失败不回退词库 ---
		{"仅模型_命中", ModeModelOnly, missText, "block", false, true, decidedByModel, 1},
		{"仅模型_未命中", ModeModelOnly, hitText, "pass", false, false, decidedByNone, 1},
		// 关键: 词库本可命中, 但仅模型模式下技术失败不回退, 结果为降级而非拦截。
		{"仅模型_技术失败不回退词库", ModeModelOnly, hitText, "", true, false, decidedByDegraded, 1},

		// --- 模型优先: 仅技术失败时回退词库 ---
		{"模型优先_模型命中", ModeModelFirst, missText, "block", false, true, decidedByModel, 1},
		// 关键: 模型未命中 ≠ 失败, 默认不回退词库, 即使词库能命中。
		{"模型优先_模型未命中不回退", ModeModelFirst, hitText, "pass", false, false, decidedByNone, 1},
		// 关键: 技术失败才回退, 且词库命中后结论归因词库。
		{"模型优先_技术失败回退词库命中", ModeModelFirst, hitText, "", true, true, decidedByWords, 1},
		{"模型优先_技术失败回退词库未命中", ModeModelFirst, missText, "", true, false, decidedByWords, 1},

		// --- 词库优先: 词库不会技术失败, 默认根本不调模型 ---
		{"词库优先_命中", ModeWordFirst, hitText, "block", false, true, decidedByWords, 0},
		{"词库优先_未命中默认不调模型", ModeWordFirst, missText, "block", false, false, decidedByNone, 0},

		// --- 两者全跑 ---
		{"全跑_双命中", ModeBoth, hitText, "block", false, true, decidedByBoth, 1},
		{"全跑_仅词库命中", ModeBoth, hitText, "pass", false, true, decidedByWords, 1},
		{"全跑_仅模型命中", ModeBoth, missText, "block", false, true, decidedByModel, 1},
		{"全跑_都未命中", ModeBoth, missText, "pass", false, false, decidedByNone, 1},
		// 全跑下模型技术失败, 词库仍然有效。
		{"全跑_模型失败词库命中", ModeBoth, hitText, "", true, true, decidedByWords, 1},
		{"全跑_模型失败词库未命中", ModeBoth, missText, "", true, false, decidedByDegraded, 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newDetectHandler(t)
			client, calls := stubModel(t, tc.modelAction, tc.modelFails)
			h.SetModelClient(client)
			h.SetDetectMode(tc.mode, false) // 召回默认关闭

			data := decodeCheck(t, do(h, http.MethodPost, "/check", tc.body, nil))

			if data.Blocked != tc.wantBlocked {
				t.Errorf("blocked = %v, 期望 %v", data.Blocked, tc.wantBlocked)
			}
			if data.DecidedBy != tc.wantDecider {
				t.Errorf("decided_by = %q, 期望 %q", data.DecidedBy, tc.wantDecider)
			}
			if *calls != tc.wantCalls {
				t.Errorf("模型调用次数 = %d, 期望 %d (短路未生效?)", *calls, tc.wantCalls)
			}
			if data.DetectMode != string(tc.mode) {
				t.Errorf("detect_mode = %q, 期望 %q", data.DetectMode, tc.mode)
			}
		})
	}
}

// 召回开关: 未命中时补跑另一条链路。
func TestRecallOnMiss(t *testing.T) {
	const hitText = `{"text":"挖矿教程"}`
	const missText = `{"text":"今天天气不错"}`

	cases := []struct {
		name        string
		mode        DetectMode
		body        string
		modelAction string
		modelFails  bool
		wantBlocked bool
		wantDecider string
		wantRecall  bool
		wantCalls   int
	}{
		// 词库优先 + 召回: 词库未命中 → 补跑模型。
		{"词库优先_召回补跑模型命中", ModeWordFirst, missText, "block", false, true, decidedByModel, true, 1},
		{"词库优先_召回补跑模型未命中", ModeWordFirst, missText, "pass", false, false, decidedByNone, true, 1},
		// 词库已命中则无需补跑。
		{"词库优先_命中不触发召回", ModeWordFirst, hitText, "block", false, true, decidedByWords, false, 0},

		// 模型优先 + 召回: 模型未命中 → 补跑词库。
		{"模型优先_召回补跑词库命中", ModeModelFirst, hitText, "pass", false, true, decidedByWords, true, 1},
		{"模型优先_召回补跑词库未命中", ModeModelFirst, missText, "pass", false, false, decidedByNone, true, 1},
		// 关键: 技术失败走降级路径, 不是召回路径。
		{"模型优先_技术失败不算召回", ModeModelFirst, hitText, "", true, true, decidedByWords, false, 1},

		// 仅模型 + 召回: 模型未命中也补跑词库。
		{"仅模型_召回补跑词库命中", ModeModelOnly, hitText, "pass", false, true, decidedByWords, true, 1},
		// 仅模型 + 召回, 但技术失败时不补跑 (失败≠未命中)。
		{"仅模型_技术失败不补跑", ModeModelOnly, hitText, "", true, false, decidedByDegraded, false, 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newDetectHandler(t)
			client, calls := stubModel(t, tc.modelAction, tc.modelFails)
			h.SetModelClient(client)
			h.SetDetectMode(tc.mode, true) // 召回开启

			data := decodeCheck(t, do(h, http.MethodPost, "/check", tc.body, nil))

			if data.Blocked != tc.wantBlocked {
				t.Errorf("blocked = %v, 期望 %v", data.Blocked, tc.wantBlocked)
			}
			if data.DecidedBy != tc.wantDecider {
				t.Errorf("decided_by = %q, 期望 %q", data.DecidedBy, tc.wantDecider)
			}
			if data.RecallTriggered != tc.wantRecall {
				t.Errorf("recall_triggered = %v, 期望 %v", data.RecallTriggered, tc.wantRecall)
			}
			if *calls != tc.wantCalls {
				t.Errorf("模型调用次数 = %d, 期望 %d", *calls, tc.wantCalls)
			}
		})
	}
}

// 请求体 mode 覆盖进程级默认值。
func TestRequestOverridesProcessMode(t *testing.T) {
	h := newDetectHandler(t)
	client, calls := stubModel(t, "block", false)
	h.SetModelClient(client)
	h.SetDetectMode(ModeWordOnly, false) // 进程级: 仅词库

	// 请求体覆盖为 model_only, 应当调用模型。
	data := decodeCheck(t, do(h, http.MethodPost, "/check",
		`{"text":"今天天气不错","mode":"model_only"}`, nil))
	if !data.Blocked || data.DecidedBy != decidedByModel {
		t.Errorf("覆盖模式未生效: blocked=%v decided_by=%q", data.Blocked, data.DecidedBy)
	}
	if *calls != 1 {
		t.Errorf("模型调用次数 = %d, 期望 1", *calls)
	}
	if data.DetectMode != string(ModeModelOnly) {
		t.Errorf("detect_mode = %q, 期望回显覆盖后的模式", data.DetectMode)
	}
}

// 请求体 recall_on_miss 覆盖, 且能显式关掉进程级开启的召回。
func TestRequestOverridesRecall(t *testing.T) {
	h := newDetectHandler(t)
	client, calls := stubModel(t, "block", false)
	h.SetModelClient(client)
	h.SetDetectMode(ModeWordFirst, true) // 进程级: 召回开启

	data := decodeCheck(t, do(h, http.MethodPost, "/check",
		`{"text":"今天天气不错","recall_on_miss":false}`, nil))
	if data.RecallTriggered {
		t.Error("显式 recall_on_miss=false 应当关闭召回")
	}
	if *calls != 0 {
		t.Errorf("模型调用次数 = %d, 期望 0", *calls)
	}
}

// 非法 mode 返回 400。
func TestInvalidModeRejected(t *testing.T) {
	h := newDetectHandler(t)
	rec := do(h, http.MethodPost, "/check", `{"text":"测试","mode":"nonsense"}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("状态码 = %d, 期望 400", rec.Code)
	}
}

// 依赖模型的模式在模型未配置时必须显式报错, 不能静默放行。
func TestModelModesRequireConfiguredService(t *testing.T) {
	for _, mode := range []DetectMode{ModeModelOnly, ModeModelFirst} {
		t.Run(string(mode), func(t *testing.T) {
			h := newDetectHandler(t) // 未 SetModelClient
			h.SetDetectMode(mode, false)
			rec := do(h, http.MethodPost, "/check", `{"text":"挖矿教程"}`, nil)
			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("状态码 = %d, 期望 503; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// both 在模型未配置时安全退化为纯词库, 保持向后兼容。
func TestBothDegradesToWordsWhenNoModel(t *testing.T) {
	h := newDetectHandler(t)
	h.SetDetectMode(ModeBoth, false)
	data := decodeCheck(t, do(h, http.MethodPost, "/check", `{"text":"挖矿教程"}`, nil))
	if !data.Blocked || data.DecidedBy != decidedByWords {
		t.Errorf("blocked=%v decided_by=%q, 期望词库判定", data.Blocked, data.DecidedBy)
	}
}

// 默认构造的 Handler 保持历史行为 (both)。
func TestDefaultModeIsBoth(t *testing.T) {
	h := newDetectHandler(t)
	if h.detectMode != ModeBoth {
		t.Errorf("默认模式 = %q, 期望 %q", h.detectMode, ModeBoth)
	}
	data := decodeCheck(t, do(h, http.MethodPost, "/check", `{"text":"挖矿教程"}`, nil))
	if !data.HasSensitiveWord || len(data.Matches) != 1 {
		t.Errorf("默认模式下词库结果字段应保持兼容: %+v", data)
	}
}

func TestParseDetectMode(t *testing.T) {
	for _, raw := range []string{"model_only", "MODEL_FIRST", " word_only ", "word_first", "both"} {
		if _, err := ParseDetectMode(raw); err != nil {
			t.Errorf("ParseDetectMode(%q) 意外报错: %v", raw, err)
		}
	}
	for _, raw := range []string{"", "nope", "model"} {
		if _, err := ParseDetectMode(raw); err == nil {
			t.Errorf("ParseDetectMode(%q) 应当报错", raw)
		}
	}
}
