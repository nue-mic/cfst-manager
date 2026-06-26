package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/nue-mic/cfst-manager/internal/api/apiresp"
)

// ListIPSources 列出 IP 源文件。
func (h *Handlers) ListIPSources(w http.ResponseWriter, r *http.Request) {
	list, err := h.Sources.List()
	if err != nil {
		apiresp.Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	apiresp.OK(w, list)
}

// ReadIPSource 读取 IP 源内容。
func (h *Handlers) ReadIPSource(w http.ResponseWriter, r *http.Request) {
	content, err := h.Sources.Read(chi.URLParam(r, "name"))
	if err != nil {
		h.respondStoreErr(w, err)
		return
	}
	apiresp.OK(w, map[string]any{"name": chi.URLParam(r, "name"), "content": content})
}

type ipSourceReq struct {
	Content string `json:"content"`
}

// WriteIPSource 新建/覆盖 IP 源。支持 JSON {content} 或纯文本 body。
func (h *Handlers) WriteIPSource(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var content string
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		var req ipSourceReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			apiresp.Err(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
			return
		}
		content = req.Content
	} else {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		content = string(b)
	}
	if err := h.Sources.Write(name, content); err != nil {
		apiresp.Err(w, http.StatusBadRequest, err.Error())
		return
	}
	apiresp.OK(w, map[string]any{"saved": true, "name": name})
}

// DeleteIPSource 删除 IP 源（内置不可删）。
func (h *Handlers) DeleteIPSource(w http.ResponseWriter, r *http.Request) {
	if err := h.Sources.Delete(chi.URLParam(r, "name")); err != nil {
		h.respondStoreErr(w, err)
		return
	}
	apiresp.OK(w, map[string]any{"deleted": true})
}
