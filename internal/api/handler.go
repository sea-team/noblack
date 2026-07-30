package api

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"noblack/internal/matcher"
	"noblack/internal/modelclient"
	"noblack/internal/stats"
	"noblack/internal/store"
)

// Handler 聚合所有 HTTP 处理逻辑, 持有 Store 与统计收集器。
type Handler struct {
	store   *store.Store
	metrics *stats.Collector
	token   string // 词条写操作的鉴权令牌; 为空表示不鉴权
	models  *modelclient.Client
}

const (
	normalRequestBodyLimit  = 3 << 20
	maximumRequestBodyLimit = 10 << 20
	defaultWordsPageSize    = 50
	maximumWordsPageSize    = 200
)

func positiveQueryInt(r *http.Request, name string, defaultValue, maximum int) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return defaultValue, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || (maximum > 0 && value > maximum) {
		return 0, errors.New(name + " 必须是有效的正整数")
	}
	return value, nil
}

// NewHandler 创建 Handler。token 为空时词条写操作不鉴权 (向后兼容)。
func NewHandler(s *store.Store, m *stats.Collector, token string) *Handler {
	return &Handler{store: s, metrics: m, token: token}
}

// SetModelClient enables dual-model inference for /check.
func (h *Handler) SetModelClient(client *modelclient.Client) {
	h.models = client
}

// ---------- 鉴权 ----------

// authEnabled 报告是否启用了写操作鉴权。
func (h *Handler) authEnabled() bool { return h.token != "" }

// tokenFromRequest 从请求中提取令牌, 支持两种写法:
//   - X-Auth-Token: <token>
//   - Authorization: Bearer <token>
func tokenFromRequest(r *http.Request) string {
	if t := r.Header.Get("X-Auth-Token"); t != "" {
		return t
	}
	if a := r.Header.Get("Authorization"); a != "" {
		return strings.TrimSpace(strings.TrimPrefix(a, "Bearer "))
	}
	return ""
}

// checkAuth 校验请求令牌。未启用鉴权时恒通过。
// 用 subtle.ConstantTimeCompare 做定长比较, 避免计时侧信道。
func (h *Handler) checkAuth(r *http.Request) bool {
	if !h.authEnabled() {
		return true
	}
	got := tokenFromRequest(r)
	return subtle.ConstantTimeCompare([]byte(got), []byte(h.token)) == 1
}

// requireAuth 校验失败时写 401 并返回 false。
func (h *Handler) requireAuth(w http.ResponseWriter, r *http.Request) bool {
	if h.checkAuth(r) {
		return true
	}
	writeErr(w, http.StatusUnauthorized, "令牌无效或缺失")
	return false
}

// withRequestBodyPolicy 统一执行 API 请求体大小策略：
// 不超过 3 MiB 正常处理，超过 3 MiB 且不超过 10 MiB 时需要有效令牌，
// 超过 10 MiB 时始终拒绝。
func (h *Handler) withRequestBodyPolicy(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || r.Body == http.NoBody {
			next(w, r)
			return
		}

		if r.ContentLength > maximumRequestBodyLimit {
			writeErr(w, http.StatusRequestEntityTooLarge, "请求体超过 10 MiB 上限")
			return
		}
		if r.ContentLength > normalRequestBodyLimit && (!h.authEnabled() || !h.checkAuth(r)) {
			writeErr(w, http.StatusUnauthorized, "请求体超过 3 MiB，需要有效令牌")
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, maximumRequestBodyLimit+1))
		_ = r.Body.Close()
		if err != nil {
			writeErr(w, http.StatusBadRequest, "读取请求体失败")
			return
		}
		if len(body) > maximumRequestBodyLimit {
			writeErr(w, http.StatusRequestEntityTooLarge, "请求体超过 10 MiB 上限")
			return
		}
		if len(body) > normalRequestBodyLimit && (!h.authEnabled() || !h.checkAuth(r)) {
			writeErr(w, http.StatusUnauthorized, "请求体超过 3 MiB，需要有效令牌")
			return
		}

		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		next(w, r)
	}
}

// Register 注册所有路由。
func (h *Handler) Register(mux *http.ServeMux) {
	handle := func(pattern string, fn http.HandlerFunc) {
		mux.HandleFunc(pattern, h.withRequestBodyPolicy(fn))
	}

	handle("/check", h.handleCheck)
	handle("/reload", h.handleReload)
	handle("/levels", h.handleLevels)
	handle("/health", h.handleHealth)
	handle("/words", h.handleWords)
	handle("/words/", h.handleWordByID)
	handle("/stats", h.handleStats)
	handle("/stats/reset", h.handleStatsReset)
	handle("/auth/status", h.handleAuthStatus)
	handle("/auth/verify", h.handleAuthVerify)
	handle("/", h.handleIndex)
}

// ---------- 统一响应 ----------

type apiResponse struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func writeJSON(w http.ResponseWriter, httpStatus int, resp apiResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(httpStatus)
	_ = json.NewEncoder(w).Encode(resp)
}

func writeErr(w http.ResponseWriter, httpStatus int, msg string) {
	writeJSON(w, httpStatus, apiResponse{Code: httpStatus, Message: msg})
}

// ---------- /check ----------

type checkRequest struct {
	Text string `json:"text"`
}

type position struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type matchItem struct {
	Word     string   `json:"word"`
	Levels   []string `json:"levels"`
	Remarks  []string `json:"remarks"`
	Position position `json:"position"`
}

type checkData struct {
	HasSensitiveWord   bool                      `json:"has_sensitive_word"`
	Matches            []matchItem               `json:"matches"`
	ModelResults       []modelclient.ModelResult `json:"model_results,omitempty"`
	CombinedAction     string                    `json:"combined_action,omitempty"`
	ModelCombinePolicy string                    `json:"model_combine_policy,omitempty"`
	ModelDevice        string                    `json:"model_device,omitempty"`
	ModelsParallel     bool                      `json:"models_parallel,omitempty"`
	ModelLatencyMS     float64                   `json:"model_latency_ms,omitempty"`
	ModelError         string                    `json:"model_error,omitempty"`
}

func (h *Handler) handleCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}

	var req checkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}

	// 无锁获取当前自动机并匹配。
	if strings.TrimSpace(req.Text) == "" {
		writeErr(w, http.StatusBadRequest, "text 不能为空或仅包含空白字符")
		return
	}

	type modelOutcome struct {
		prediction *modelclient.Prediction
		err        error
	}
	var modelCh chan modelOutcome
	if h.models != nil {
		modelCh = make(chan modelOutcome, 1)
		modelContext := context.WithoutCancel(r.Context())
		go func() {
			prediction, err := h.models.Check(modelContext, req.Text)
			modelCh <- modelOutcome{prediction: prediction, err: err}
		}()
	}

	rawMatches := h.store.Current().FindAll(req.Text)

	items := make([]matchItem, 0, len(rawMatches))
	hitWords := make([]string, 0, len(rawMatches))
	for _, m := range rawMatches {
		levels := m.Levels
		if levels == nil {
			levels = []string{}
		}
		remarks := m.Remarks
		if remarks == nil {
			remarks = []string{}
		}
		items = append(items, matchItem{
			Word:     m.Word,
			Levels:   levels,
			Remarks:  remarks,
			Position: position{Start: m.Start, End: m.End},
		})
		hitWords = append(hitWords, m.Word)
	}

	// 记录统计 (原子操作, ~纳秒级, 不影响吞吐)。
	h.metrics.RecordCheck(hitWords)

	data := checkData{HasSensitiveWord: len(items) > 0, Matches: items}
	if modelCh != nil {
		outcome := <-modelCh
		if outcome.err != nil {
			log.Printf("[models] inference failed: %v", outcome.err)
			data.ModelError = "model service unavailable"
		} else {
			data.ModelResults = outcome.prediction.Models
			data.CombinedAction = outcome.prediction.CombinedAction
			data.ModelCombinePolicy = outcome.prediction.CombinePolicy
			data.ModelDevice = outcome.prediction.Device
			data.ModelsParallel = outcome.prediction.Parallel
			data.ModelLatencyMS = outcome.prediction.LatencyMilliseconds
		}
	}

	writeJSON(w, http.StatusOK, apiResponse{
		Code:    200,
		Message: "success",
		Data:    data,
	})
}

// ---------- /reload ----------

func (h *Handler) handleReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	if !h.requireAuth(w, r) {
		return
	}
	n, err := h.store.Reload()
	if err != nil {
		log.Printf("[reload] 失败: %v", err)
		writeErr(w, http.StatusInternalServerError, "热加载失败: "+err.Error())
		return
	}
	h.metrics.RecordReload()
	log.Printf("[reload] 成功, 词条数: %d", n)
	writeJSON(w, http.StatusOK, apiResponse{Code: 200, Message: "reloaded", Data: map[string]int{"word_count": n}})
}

// ---------- /levels ----------

func (h *Handler) handleLevels(w http.ResponseWriter, r *http.Request) {
	levels := h.store.Current().Levels()
	if levels == nil {
		levels = []string{}
	}
	writeJSON(w, http.StatusOK, apiResponse{
		Code: 200, Message: "success",
		Data: map[string]interface{}{"levels": levels, "count": len(levels)},
	})
}

// ---------- /health ----------

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	auto := h.store.Current()
	levels := auto.Levels()
	if levels == nil {
		levels = []string{}
	}
	writeJSON(w, http.StatusOK, apiResponse{
		Code: 200, Message: "ok",
		Data: map[string]interface{}{"word_count": auto.Size(), "levels": levels},
	})
}

// ---------- /words (GET 列表 / POST 新增) ----------

func (h *Handler) handleWords(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		page, err := positiveQueryInt(r, "page", 1, 0)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		pageSize, err := positiveQueryInt(r, "page_size", defaultWordsPageSize, maximumWordsPageSize)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		match := r.URL.Query().Get("match")
		if match == "" {
			match = "contains"
		}
		if match != "contains" && match != "exact" && match != "prefix" && match != "suffix" {
			writeErr(w, http.StatusBadRequest, "match 必须是 contains、exact、prefix 或 suffix")
			return
		}
		result := h.store.ListEntriesPageMatch(page, pageSize, r.URL.Query().Get("q"), match)
		totalPages := 0
		if result.Total > 0 {
			totalPages = (result.Total + pageSize - 1) / pageSize
		}
		writeJSON(w, http.StatusOK, apiResponse{
			Code: 200, Message: "success",
			Data: map[string]interface{}{
				"words": result.Entries, "count": result.Total,
				"page": page, "page_size": pageSize, "total_pages": totalPages,
			},
		})
	case http.MethodPost:
		if !h.requireAuth(w, r) { // 写操作: 需令牌
			return
		}
		var e matcher.Entry
		if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
			writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
			return
		}
		e = matcher.NormalizeEntry(e) // normalize before persistence
		result, err := h.store.AddOrMergeEntry(e)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		message := "created"
		if result.Merged {
			message = "merged"
		}
		log.Printf("[words] %s entry: %s", message, result.Entry.Word)
		writeJSON(w, http.StatusOK, apiResponse{
			Code: 200, Message: message,
			Data: map[string]interface{}{
				"word": result.Entry.Word, "levels": result.Entry.Levels, "remarks": result.Entry.Remarks,
				"created": result.Created, "merged": result.Merged,
				"added_words": result.AddedWords, "reused_words": result.ReusedWords,
			},
		})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET / POST")
	}
}

// ---------- /words/{word} (PUT 更新 / DELETE 删除) ----------

func (h *Handler) handleWordByID(w http.ResponseWriter, r *http.Request) {
	// 路径形如 /words/挖矿, 取 /words/ 之后的部分。
	// r.URL.Path 已由 net/http 完成 URL 解码, 中文可直接使用。
	word := matcher.NormalizeWord(trimPrefixPath(r.URL.Path, "/words/"))
	if word == "" {
		writeErr(w, http.StatusBadRequest, "缺少词条, 路径应为 /words/{word}")
		return
	}

	switch r.Method {
	case http.MethodPut:
		if !h.requireAuth(w, r) { // 写操作: 需令牌
			return
		}
		var e matcher.Entry
		if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
			writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
			return
		}
		if e.Word != "" && matcher.NormalizeWord(e.Word) != word {
			writeErr(w, http.StatusBadRequest, "请求体中的敏感词字段必须与路径中的词条一致")
			return
		}
		e.Word = word
		e = matcher.NormalizeEntry(e) // 清洗后再存, 保证响应与落盘一致
		if err := h.store.UpdateEntry(e); err != nil {
			if errors.Is(err, store.ErrEntryNotFound) {
				writeErr(w, http.StatusNotFound, err.Error())
			} else {
				writeErr(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		log.Printf("[words] 更新词条: %s", e.Word)
		writeJSON(w, http.StatusOK, apiResponse{Code: 200, Message: "updated", Data: e})
	case http.MethodDelete:
		if !h.requireAuth(w, r) { // 写操作: 需令牌
			return
		}
		if err := h.store.DeleteEntry(word); err != nil {
			if errors.Is(err, store.ErrEntryNotFound) {
				writeErr(w, http.StatusNotFound, err.Error())
			} else {
				writeErr(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		log.Printf("[words] 删除词条: %s", word)
		writeJSON(w, http.StatusOK, apiResponse{Code: 200, Message: "deleted", Data: map[string]string{"word": word}})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 PUT / DELETE")
	}
}

// ---------- /stats ----------

func (h *Handler) handleStats(w http.ResponseWriter, r *http.Request) {
	page, err := positiveQueryInt(r, "page", 1, 0)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	pageSize := 20
	if r.URL.Query().Get("page_size") != "" {
		pageSize, err = positiveQueryInt(r, "page_size", pageSize, 200)
	} else if r.URL.Query().Get("top") != "" {
		// 兼容旧版 top=N：把 N 作为每页条数。
		pageSize, err = positiveQueryInt(r, "top", pageSize, 200)
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	snap := h.metrics.SnapshotPage(page, pageSize)
	writeJSON(w, http.StatusOK, apiResponse{Code: 200, Message: "success", Data: snap})
}

func (h *Handler) handleStatsReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	if !h.requireAuth(w, r) {
		return
	}
	h.metrics.Reset()
	writeJSON(w, http.StatusOK, apiResponse{Code: 200, Message: "reset"})
}

// ---------- 鉴权状态 / 校验 ----------

// handleAuthStatus GET /auth/status: 告诉前端是否需要令牌 (是否启用了写鉴权)。
func (h *Handler) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, apiResponse{
		Code: 200, Message: "success",
		Data: map[string]bool{"required": h.authEnabled()},
	})
}

// handleAuthVerify POST /auth/verify: 校验请求携带的令牌是否正确, 供前端"验证"按钮使用。
// 未启用鉴权时恒返回成功。
func (h *Handler) handleAuthVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	if !h.checkAuth(r) {
		writeErr(w, http.StatusUnauthorized, "令牌无效或缺失")
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Code: 200, Message: "ok"})
}

// ---------- 前端页面 ----------

func (h *Handler) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(indexHTML))
}
