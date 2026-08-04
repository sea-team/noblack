package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"noblack/internal/samples"
)

// 语义样本库接口。
//
// 用途: 模型权重无法在线更新, 遇到漏报只能等下一轮微调。词库能补单词, 但补不了
// "换几个字的同类句式"。样本库让运营把漏报的整句提交上来, 立即生效 —— 检测时
// 用字符 n-gram 相似度召回相似句式。
//
//	GET    /samples          列出全部样本
//	POST   /samples          新增样本 (需鉴权)
//	PUT    /samples/{id}     修改样本 (需鉴权)
//	DELETE /samples/{id}     删除样本 (需鉴权)

type addSampleRequest struct {
	Text   string   `json:"text"`
	Levels []string `json:"levels"`
	Remark string   `json:"remark"`
}

func (h *Handler) handleSamples(w http.ResponseWriter, r *http.Request) {
	if h.samples == nil {
		writeErr(w, http.StatusServiceUnavailable, "语义样本库未启用 (需设置 -samples-file)")
		return
	}

	switch r.Method {
	case http.MethodGet:
		list := h.samples.List()
		writeJSON(w, http.StatusOK, apiResponse{
			Code: 200, Message: "success",
			Data: map[string]interface{}{
				"samples":   list,
				"count":     len(list),
				"threshold": h.samples.Threshold(),
			},
		})

	case http.MethodPost:
		if !h.requireAuth(w, r) {
			return
		}
		var req addSampleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
			return
		}
		sample, added, err := h.samples.Add(req.Text, req.Levels, req.Remark)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		// 重复提交不算错误: 返回已有样本并用 added 标明未新增,
		// 便于调用方区分 "刚加进去" 与 "早就有了"。
		status := http.StatusCreated
		message := "success"
		if !added {
			status = http.StatusOK
			message = "样本已存在"
		}
		writeJSON(w, status, apiResponse{
			Code: 200, Message: message,
			Data: map[string]interface{}{"sample": sample, "added": added},
		})

	default:
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET 与 POST")
	}
}

func (h *Handler) handleSampleByID(w http.ResponseWriter, r *http.Request) {
	if h.samples == nil {
		writeErr(w, http.StatusServiceUnavailable, "语义样本库未启用 (需设置 -samples-file)")
		return
	}
	if r.Method != http.MethodDelete && r.Method != http.MethodPut {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 PUT 与 DELETE")
		return
	}
	if !h.requireAuth(w, r) {
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/samples/")
	if id == "" || strings.Contains(id, "/") {
		writeErr(w, http.StatusBadRequest, "样本 ID 无效")
		return
	}

	if r.Method == http.MethodPut {
		var req addSampleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
			return
		}
		updated, err := h.samples.Update(id, req.Text, req.Levels, req.Remark)
		if err != nil {
			if samples.IsNotFound(err) {
				writeErr(w, http.StatusNotFound, "样本不存在: "+id)
				return
			}
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{
			Code: 200, Message: "success",
			Data: map[string]interface{}{"sample": updated},
		})
		return
	}

	removed, err := h.samples.Delete(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "删除失败: "+err.Error())
		return
	}
	if !removed {
		writeErr(w, http.StatusNotFound, "样本不存在: "+id)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{
		Code: 200, Message: "success",
		Data: map[string]interface{}{"id": id, "deleted": true},
	})
}
