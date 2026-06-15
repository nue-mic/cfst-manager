package api

import (
	"encoding/json"
	"net/http"

	"github.com/mia-clark/cfst-manager/internal/api/apiresp"
	"github.com/mia-clark/cfst-manager/internal/store"
)

// GetSettings 返回全局设置。
func (h *Handlers) GetSettings(w http.ResponseWriter, r *http.Request) {
	apiresp.OK(w, h.Store.GetSettings())
}

// UpdateSettings 覆盖全局设置。
func (h *Handlers) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var set store.Settings
	if err := json.NewDecoder(r.Body).Decode(&set); err != nil {
		apiresp.Err(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if err := h.Store.UpdateSettings(set); err != nil {
		apiresp.Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	apiresp.OK(w, h.Store.GetSettings())
}
