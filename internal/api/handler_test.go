package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"noblack/internal/matcher"
	"noblack/internal/modelclient"
	"noblack/internal/stats"
	"noblack/internal/store"
)

func newTestHandler(t *testing.T, token string) *Handler {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/words.json"
	entries := []matcher.Entry{{Word: "挖矿", Levels: []string{"L"}}}
	if err := matcher.SaveEntries(path, entries); err != nil {
		t.Fatal(err)
	}
	st := store.New(path, entries, matcher.Options{})
	return NewHandler(st, stats.New(), token)
}

func newHandlerWithEntries(t *testing.T, entries []matcher.Entry) *Handler {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/words.json"
	if err := matcher.SaveEntries(path, entries); err != nil {
		t.Fatal(err)
	}
	return NewHandler(store.New(path, entries, matcher.Options{}), stats.New(), "")
}

func do(h *Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	mux := http.NewServeMux()
	h.Register(mux)
	mux.ServeHTTP(rec, req)
	return rec
}

func TestAuth_WritesBlockedWithoutToken(t *testing.T) {
	h := newTestHandler(t, "s3cret")

	// 无令牌新增 -> 401
	if rec := do(h, "POST", "/words", `{"word":"x","levels":["A"]}`, nil); rec.Code != 401 {
		t.Errorf("无令牌 POST 应 401, 实际 %d", rec.Code)
	}
	// 错令牌 -> 401
	if rec := do(h, "POST", "/words", `{"word":"x","levels":["A"]}`, map[string]string{"X-Auth-Token": "bad"}); rec.Code != 401 {
		t.Errorf("错令牌 POST 应 401, 实际 %d", rec.Code)
	}
	// 正确令牌 (X-Auth-Token) -> 200
	if rec := do(h, "POST", "/words", `{"word":"x","levels":["A"]}`, map[string]string{"X-Auth-Token": "s3cret"}); rec.Code != 200 {
		t.Errorf("正确令牌 POST 应 200, 实际 %d: %s", rec.Code, rec.Body)
	}
	// 正确令牌 (Bearer) -> 200
	if rec := do(h, "PUT", "/words/x", `{"levels":["B"]}`, map[string]string{"Authorization": "Bearer s3cret"}); rec.Code != 200 {
		t.Errorf("Bearer 令牌 PUT 应 200, 实际 %d", rec.Code)
	}
	// 无令牌删除 -> 401
	if rec := do(h, "DELETE", "/words/x", "", nil); rec.Code != 401 {
		t.Errorf("无令牌 DELETE 应 401, 实际 %d", rec.Code)
	}
}

func TestAuth_ReadsAlwaysOpen(t *testing.T) {
	h := newTestHandler(t, "s3cret")
	// GET /words 不需令牌
	if rec := do(h, "GET", "/words", "", nil); rec.Code != 200 {
		t.Errorf("GET /words 应 200, 实际 %d", rec.Code)
	}
	// /check 不需令牌
	if rec := do(h, "POST", "/check", `{"text":"挖矿"}`, nil); rec.Code != 200 {
		t.Errorf("/check 应 200, 实际 %d", rec.Code)
	}
	// /stats 不需令牌
	if rec := do(h, "GET", "/stats", "", nil); rec.Code != 200 {
		t.Errorf("/stats 应 200, 实际 %d", rec.Code)
	}
}

func TestWordsGetUsesServerPaginationAndSearch(t *testing.T) {
	entries := make([]matcher.Entry, 0, 60)
	for index := 60; index >= 1; index-- {
		entry := matcher.Entry{Word: fmt.Sprintf("word-%02d", index), Levels: []string{"common"}}
		if index == 23 {
			entry.Remarks = []string{"special remark"}
		}
		entries = append(entries, entry)
	}
	h := newHandlerWithEntries(t, entries)

	type wordsData struct {
		Words      []matcher.Entry `json:"words"`
		Count      int             `json:"count"`
		Page       int             `json:"page"`
		PageSize   int             `json:"page_size"`
		TotalPages int             `json:"total_pages"`
	}
	getData := func(path string) (int, wordsData) {
		rec := do(h, http.MethodGet, path, "", nil)
		var response struct {
			Data wordsData `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode %s: %v; body=%s", path, err, rec.Body)
		}
		return rec.Code, response.Data
	}

	status, data := getData("/words")
	if status != http.StatusOK || len(data.Words) != 50 || data.Count != 60 ||
		data.Page != 1 || data.PageSize != 50 || data.TotalPages != 2 {
		t.Fatalf("default page status=%d data=%+v", status, data)
	}
	if data.Words[0].Word != "word-01" || data.Words[49].Word != "word-50" {
		t.Fatalf("default page words=%q..%q", data.Words[0].Word, data.Words[49].Word)
	}

	status, data = getData("/words?page=2&page_size=20")
	if status != http.StatusOK || len(data.Words) != 20 || data.Page != 2 || data.PageSize != 20 || data.TotalPages != 3 {
		t.Fatalf("custom page status=%d data=%+v", status, data)
	}
	if data.Words[0].Word != "word-21" || data.Words[19].Word != "word-40" {
		t.Fatalf("custom page words=%q..%q", data.Words[0].Word, data.Words[19].Word)
	}

	status, data = getData("/words?page_size=10&q=SPECIAL")
	if status != http.StatusOK || data.Count != 1 || len(data.Words) != 1 || data.Words[0].Word != "word-23" {
		t.Fatalf("search result status=%d data=%+v", status, data)
	}
	status, data = getData("/words?page_size=10&q=WORD-2&match=exact")
	if status != http.StatusOK || data.Count != 0 {
		t.Fatalf("exact search status=%d data=%+v", status, data)
	}
	status, data = getData("/words?page_size=10&q=WORD-2&match=prefix")
	if status != http.StatusOK || data.Count != 10 || data.Words[0].Word != "word-20" {
		t.Fatalf("prefix search status=%d data=%+v", status, data)
	}
}

func TestStatsGetUsesServerPagination(t *testing.T) {
	h := newTestHandler(t, "")
	for index := 5; index >= 1; index-- {
		for count := 0; count < index; count++ {
			h.metrics.RecordCheck([]string{fmt.Sprintf("word-%d", index)})
		}
	}
	rec := do(h, http.MethodGet, "/stats?page=2&page_size=2", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /stats status=%d body=%s", rec.Code, rec.Body)
	}
	var response struct {
		Data stats.Snapshot `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Page != 2 || response.Data.PageSize != 2 || response.Data.TotalPages != 3 ||
		len(response.Data.TopWords) != 2 || response.Data.TopWords[0].Word != "word-3" {
		t.Fatalf("stats page=%+v", response.Data)
	}
}

func TestStatsGetRejectsInvalidPagination(t *testing.T) {
	h := newTestHandler(t, "")
	for _, path := range []string{"/stats?page=0", "/stats?page_size=0", "/stats?page_size=201"} {
		if rec := do(h, http.MethodGet, path, "", nil); rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s status=%d, want 400", path, rec.Code)
		}
	}
}

func TestWordsGetRejectsInvalidPagination(t *testing.T) {
	h := newTestHandler(t, "")
	for _, path := range []string{
		"/words?page=0",
		"/words?page=-1",
		"/words?page=abc",
		"/words?page_size=0",
		"/words?page_size=201",
		"/words?page_size=abc",
		"/words?match=unknown",
	} {
		rec := do(h, http.MethodGet, path, "", nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s status=%d body=%s, want 400", path, rec.Code, rec.Body)
		}
	}
}

func TestCheckRejectsMissingEmptyAndWhitespaceText(t *testing.T) {
	h := newTestHandler(t, "")
	cases := []string{
		`{}`,
		`{"text":""}`,
		`{"text":"   \t\n"}`,
	}
	for _, body := range cases {
		rec := do(h, http.MethodPost, "/check", body, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body=%s actual status=%d, expected 400: %s", body, rec.Code, rec.Body)
		}
		if !strings.Contains(rec.Body.String(), `"code":400`) || !strings.Contains(rec.Body.String(), "text 不能为空") {
			t.Errorf("body=%s unexpected response: %s", body, rec.Body)
		}
	}
}

func TestAuth_DisabledWhenNoToken(t *testing.T) {
	h := newTestHandler(t, "") // 未设令牌 = 不鉴权
	if rec := do(h, "POST", "/words", `{"word":"y","levels":["A"]}`, nil); rec.Code != 200 {
		t.Errorf("未启用鉴权时 POST 应 200, 实际 %d: %s", rec.Code, rec.Body)
	}
	// /auth/status 应报告 required:false
	rec := do(h, "GET", "/auth/status", "", nil)
	if !strings.Contains(rec.Body.String(), `"required":false`) {
		t.Errorf("未启用鉴权 status 应 required:false, 实际 %s", rec.Body)
	}
}

func TestAuth_VerifyEndpoint(t *testing.T) {
	h := newTestHandler(t, "s3cret")
	if rec := do(h, "POST", "/auth/verify", "", map[string]string{"X-Auth-Token": "s3cret"}); rec.Code != 200 {
		t.Errorf("正确令牌 verify 应 200, 实际 %d", rec.Code)
	}
	if rec := do(h, "POST", "/auth/verify", "", map[string]string{"X-Auth-Token": "nope"}); rec.Code != 401 {
		t.Errorf("错令牌 verify 应 401, 实际 %d", rec.Code)
	}
}

func TestAuth_ManagementWritesRequireToken(t *testing.T) {
	h := newTestHandler(t, "s3cret")
	for _, path := range []string{"/reload", "/stats/reset"} {
		if rec := do(h, http.MethodPost, path, "", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s 未携带令牌，实际状态码 %d，期望 401", path, rec.Code)
		}
		if rec := do(h, http.MethodPost, path, "", map[string]string{"X-Auth-Token": "s3cret"}); rec.Code != http.StatusOK {
			t.Errorf("%s 携带正确令牌，实际状态码 %d，期望 200: %s", path, rec.Code, rec.Body)
		}
	}
}

func TestRequestBodyPolicy(t *testing.T) {
	payload := func(size int) string {
		const prefix = `{"text":"`
		const suffix = `"}`
		return prefix + strings.Repeat("a", size-len(prefix)-len(suffix)) + suffix
	}

	h := newTestHandler(t, "s3cret")
	if rec := do(h, http.MethodPost, "/check", payload(normalRequestBodyLimit), nil); rec.Code != http.StatusOK {
		t.Fatalf("3 MiB 请求实际状态码 %d，期望 200: %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodPost, "/check", payload(normalRequestBodyLimit+1), nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("超过 3 MiB 且未携带令牌，实际状态码 %d，期望 401", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/check", payload(normalRequestBodyLimit+1), map[string]string{"X-Auth-Token": "s3cret"}); rec.Code != http.StatusOK {
		t.Fatalf("超过 3 MiB 且携带正确令牌，实际状态码 %d，期望 200: %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodPost, "/check", payload(maximumRequestBodyLimit+1), map[string]string{"X-Auth-Token": "s3cret"}); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超过 10 MiB，实际状态码 %d，期望 413", rec.Code)
	}

	withoutConfiguredToken := newTestHandler(t, "")
	if rec := do(withoutConfiguredToken, http.MethodPost, "/check", payload(normalRequestBodyLimit+1), nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("服务未配置令牌时发送超过 3 MiB 请求，实际状态码 %d，期望 401", rec.Code)
	}
}

func TestRequestBodyPolicyChunkedUnknownLength(t *testing.T) {
	payload := func(size int) string {
		const prefix = `{"text":"`
		const suffix = `"}`
		return prefix + strings.Repeat("a", size-len(prefix)-len(suffix)) + suffix
	}
	callUnknownLength := func(h *Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/check", strings.NewReader(body))
		req.ContentLength = -1
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		mux := http.NewServeMux()
		h.Register(mux)
		mux.ServeHTTP(rec, req)
		return rec
	}

	h := newTestHandler(t, "s3cret")
	if rec := callUnknownLength(h, payload(normalRequestBodyLimit+1), nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("未知长度请求超过 3 MiB 且未携带令牌，实际状态码 %d，期望 401", rec.Code)
	}
	if rec := callUnknownLength(h, payload(maximumRequestBodyLimit+1), map[string]string{"X-Auth-Token": "s3cret"}); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("未知长度请求超过 10 MiB，实际状态码 %d，期望 413", rec.Code)
	}
}

func TestUpdateRejectsBodyPathMismatch(t *testing.T) {
	h := newTestHandler(t, "s3cret")
	rec := do(h, http.MethodPut, "/words/%E6%8C%96%E7%9F%BF", `{"word":"其他词","levels":["B"]}`, map[string]string{"X-Auth-Token": "s3cret"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("请求体词条与路径不一致，实际状态码 %d，期望 400: %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/words", "", nil); !strings.Contains(rec.Body.String(), `"levels":["L"]`) {
		t.Fatalf("拒绝更新后原词条发生了变化: %s", rec.Body)
	}
}

func TestIndexDoesNotEmbedWordsInInlineHandlers(t *testing.T) {
	h := newTestHandler(t, "")
	rec := do(h, http.MethodGet, "/", "", nil)
	body := rec.Body.String()
	if strings.Contains(body, "onclick='editWord(") || strings.Contains(body, "onclick='delWord(") {
		t.Fatalf("词条操作仍在使用内联事件处理器")
	}
	if !strings.Contains(body, `data-word-action="edit"`) || !strings.Contains(body, `addEventListener('click'`) {
		t.Fatalf("未找到安全的事件委托实现")
	}
}

func TestIndexUsesServerWordPagination(t *testing.T) {
	h := newTestHandler(t, "")
	rec := do(h, http.MethodGet, "/", "", nil)
	body := rec.Body.String()
	for _, marker := range []string{
		`id="w-first"`, `id="w-prev"`, `id="w-page-info"`, `id="w-next"`, `id="w-last"`,
		`id="w-page-size"`, `id="w-match"`, `模糊匹配`, `id="stat-first"`, `id="stat-page-info"`,
		`new URLSearchParams`, `page_size`, `scheduleWordSearch()`, `300`,
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("分页页面缺少标记 %q", marker)
		}
	}
	if strings.Contains(body, "ALL_WORDS") || strings.Contains(body, "RENDERED_WORDS") {
		t.Fatal("前端仍会保存或筛选完整词库")
	}
}

func TestIndexMergeFlowDoesNotDeleteStaleEditingPath(t *testing.T) {
	h := newTestHandler(t, "")
	rec := do(h, http.MethodGet, "/", "", nil)
	body := rec.Body.String()
	if !strings.Contains(body, `const saved=await api('/words'`) || !strings.Contains(body, `if(saved.merged)`) {
		t.Fatalf("前端未根据 merged 响应分支处理")
	}
	mergedStart := strings.Index(body, `if(saved.merged)`)
	elseStart := strings.Index(body[mergedStart:], `}else{`)
	if mergedStart < 0 || elseStart < 0 {
		t.Fatalf("未找到 merged/else 分支")
	}
	mergedBranch := body[mergedStart : mergedStart+elseStart]
	if strings.Contains(mergedBranch, `method:'DELETE'`) {
		t.Fatalf("merged 分支仍会 DELETE 已被替换的旧词条")
	}
}

func TestWordWriteFailureReturns500(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/words.json"
	entries := []matcher.Entry{{Word: "挖矿", Levels: []string{"L"}}}
	if err := matcher.SaveEntries(path, entries); err != nil {
		t.Fatal(err)
	}
	// 把词库路径替换为目录，使临时文件创建或重命名必然失败，用于模拟持久化错误。
	badPath := dir + "/不可写目标"
	if err := os.Mkdir(badPath, 0o755); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(store.New(badPath, entries, matcher.Options{}), stats.New(), "s3cret")
	rec := do(h, http.MethodPut, "/words/%E6%8C%96%E7%9F%BF", `{"levels":["B"]}`, map[string]string{"X-Auth-Token": "s3cret"})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("词库落盘失败时实际状态码 %d，期望 500: %s", rec.Code, rec.Body)
	}
}

func TestMissingWordStillReturns404(t *testing.T) {
	h := newTestHandler(t, "s3cret")
	rec := do(h, http.MethodPut, "/words/%E4%B8%8D%E5%AD%98%E5%9C%A8", `{"levels":["B"]}`, map[string]string{"X-Auth-Token": "s3cret"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("更新不存在词条时实际状态码 %d，期望 404: %s", rec.Code, rec.Body)
	}
}

func TestCheckIncludesBothModelResults(t *testing.T) {
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"request_id":"abc","device":"cpu","parallel":true,"combined_action":"review","combine_policy":"consensus","latency_ms":12.5,"models":[{"model":"lite","id":"1","sexual_harm_probability":0.2,"action":"review","semantic_gate":0.5,"rule_hits":[],"pass_threshold":0.15,"block_threshold":0.5,"latency_ms":2},{"model":"macbert","id":"1","sexual_harm_probability":0.8,"action":"block","semantic_gate":0.6,"rule_hits":[],"pass_threshold":0.15,"block_threshold":0.5,"latency_ms":10}]}`))
	}))
	defer modelServer.Close()

	h := newTestHandler(t, "")
	h.SetModelClient(modelclient.New(modelServer.URL, time.Second))
	rec := do(h, http.MethodPost, "/check", `{"text":"test"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/check status=%d body=%s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, expected := range []string{`"model_results"`, `"model":"lite"`, `"model":"macbert"`, `"model_device":"cpu"`, `"models_parallel":true`, `"model_combine_policy":"consensus"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("missing %s in %s", expected, body)
		}
	}
}

func TestWordsPostMergesCompatibleOverlappingBatch(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/words.json"
	entries := []matcher.Entry{{Word: "mother-a,mother-b,mother-c", Levels: []string{"Medium"}, Remarks: []string{"abuse"}}}
	if err := matcher.SaveEntries(path, entries); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(store.New(path, entries, matcher.Options{}), stats.New(), "token")
	rec := do(h, http.MethodPost, "/words", `{"word":"mother-a,mother-b,mother-c,mother-d","levels":["Medium"],"remarks":["abuse"]}`, map[string]string{"X-Auth-Token": "token"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, expected := range []string{`"message":"merged"`, `"word":"mother-a,mother-b,mother-c,mother-d"`, `"added_words":["mother-d"]`, `"reused_words":["mother-a","mother-b","mother-c"]`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("missing %s in %s", expected, body)
		}
	}
}

func TestWordsPostRejectsOverlappingMetadataConflict(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/words.json"
	entries := []matcher.Entry{{Word: "existing", Levels: []string{"A"}, Remarks: []string{"old"}}}
	if err := matcher.SaveEntries(path, entries); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(store.New(path, entries, matcher.Options{}), stats.New(), "token")
	rec := do(h, http.MethodPost, "/words", `{"word":"existing,new-word","levels":["Other"]}`, map[string]string{"X-Auth-Token": "token"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "levels/remarks differ") {
		t.Fatalf("unexpected conflict response: %s", rec.Body)
	}
}
