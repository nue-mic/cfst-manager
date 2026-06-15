package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/mia-clark/cfst-manager/internal/api/apiresp"
	"github.com/mia-clark/cfst-manager/internal/store"
)

// ListProfiles 列出全部 CDN Profile。
func (h *Handlers) ListProfiles(w http.ResponseWriter, r *http.Request) {
	apiresp.OK(w, h.Store.ListProfiles())
}

// GetProfile 返回指定 Profile。
func (h *Handlers) GetProfile(w http.ResponseWriter, r *http.Request) {
	p, ok := h.Store.GetProfile(chi.URLParam(r, "name"))
	if !ok {
		apiresp.Err(w, http.StatusNotFound, "Profile 不存在")
		return
	}
	apiresp.OK(w, p)
}

// UpsertProfile 新增/更新 Profile。
func (h *Handlers) UpsertProfile(w http.ResponseWriter, r *http.Request) {
	var p store.Profile
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		apiresp.Err(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	p.Name = chi.URLParam(r, "name")
	if p.Name == "" {
		apiresp.Err(w, http.StatusBadRequest, "Profile 名称不能为空")
		return
	}
	if err := h.Store.UpsertProfile(p); err != nil {
		apiresp.Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	apiresp.OK(w, p)
}
