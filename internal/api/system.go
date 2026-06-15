package api

import (
	"net/http"

	"github.com/mia-clark/cfst-manager/internal/api/apiresp"
)

// Health 健康探针（无需鉴权）。
func (h *Handlers) Health(w http.ResponseWriter, r *http.Request) {
	apiresp.JSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// Version 返回版本与运行信息。
func (h *Handlers) Version(w http.ResponseWriter, r *http.Request) {
	apiresp.OK(w, map[string]any{
		"version":     h.Version_(),
		"public_open": h.Store.GetSettings().PublicOpen,
	})
}

// Version_ 返回版本字符串（避免与字段名冲突的私有辅助）。
func (h *Handlers) Version_() string {
	if h.Deps.Version == "" {
		return "dev"
	}
	return h.Deps.Version
}
