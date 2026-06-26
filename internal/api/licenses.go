package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/nue-mic/cfst-manager/internal/api/apiresp"
)

// ListLicenses 列出对外授权密钥。
func (h *Handlers) ListLicenses(w http.ResponseWriter, r *http.Request) {
	apiresp.OK(w, h.Store.ListLicenses())
}

type licenseReq struct {
	Key     string `json:"key"`
	Note    string `json:"note"`
	Enabled bool   `json:"enabled"`
	Count   int64  `json:"count"`
}

// CreateLicense 新增授权密钥（key 留空自动生成）。
func (h *Handlers) CreateLicense(w http.ResponseWriter, r *http.Request) {
	var req licenseReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	lic, err := h.Store.AddLicense(req.Key, req.Note, req.Count)
	if err != nil {
		apiresp.Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	apiresp.Created(w, lic)
}

// UpdateLicense 更新授权密钥（备注/启用/余额）。
func (h *Handlers) UpdateLicense(w http.ResponseWriter, r *http.Request) {
	var req licenseReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apiresp.Err(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	key := chi.URLParam(r, "key")
	if err := h.Store.UpdateLicense(key, req.Note, req.Enabled, req.Count); err != nil {
		h.respondStoreErr(w, err)
		return
	}
	apiresp.OK(w, map[string]any{"updated": true})
}

// DeleteLicense 删除授权密钥。
func (h *Handlers) DeleteLicense(w http.ResponseWriter, r *http.Request) {
	if err := h.Store.DeleteLicense(chi.URLParam(r, "key")); err != nil {
		h.respondStoreErr(w, err)
		return
	}
	apiresp.OK(w, map[string]any{"deleted": true})
}
