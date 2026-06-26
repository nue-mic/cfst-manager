package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/nue-mic/cfst-manager/internal/api/apiresp"
	"github.com/nue-mic/cfst-manager/internal/engine"
	"github.com/nue-mic/cfst-manager/internal/scheduler"
	"github.com/nue-mic/cfst-manager/internal/store"
)

// scheduleReq 是定时任务的创建/更新请求体。
type scheduleReq struct {
	Name     string        `json:"name"`
	Enabled  bool          `json:"enabled"`
	Spec     string        `json:"spec"`
	Profile  string        `json:"profile"`
	IPSource string        `json:"ip_source"`
	IPText   string        `json:"ip_text"`
	Publish  bool          `json:"publish"`
	Note     string        `json:"note"`
	Config   engine.Config `json:"config"`
}

func (r scheduleReq) toSchedule() store.Schedule {
	profile := strings.TrimSpace(r.Profile)
	if profile == "" {
		profile = "cloudflare"
	}
	return store.Schedule{
		Name: strings.TrimSpace(r.Name), Enabled: r.Enabled, Spec: strings.TrimSpace(r.Spec),
		Profile: profile, IPSource: r.IPSource, IPText: r.IPText,
		Publish: r.Publish, Note: r.Note, Config: r.Config,
	}
}

// ListSchedules 列出全部定时任务，并附带每个任务的下次运行时间。
func (h *Handlers) ListSchedules(w http.ResponseWriter, r *http.Request) {
	apiresp.OK(w, map[string]any{
		"schedules": h.Store.ListSchedules(),
		"next_runs": h.Scheduler.NextRuns(),
	})
}

// GetSchedule 返回单个定时任务。
func (h *Handlers) GetSchedule(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.Store.GetSchedule(chi.URLParam(r, "id"))
	if !ok {
		apiresp.Err(w, http.StatusNotFound, "定时任务不存在")
		return
	}
	apiresp.OK(w, sc)
}

// CreateSchedule 新建定时任务。
func (h *Handlers) CreateSchedule(w http.ResponseWriter, r *http.Request) {
	var req scheduleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apiresp.Err(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if err := scheduler.ValidateSpec(req.Spec); err != nil {
		apiresp.Err(w, http.StatusBadRequest, "cron 表达式无效: "+err.Error())
		return
	}
	sc, err := h.Store.AddSchedule(req.toSchedule())
	if err != nil {
		apiresp.Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.Scheduler.Reload()
	apiresp.Created(w, sc)
}

// UpdateSchedule 更新定时任务。
func (h *Handlers) UpdateSchedule(w http.ResponseWriter, r *http.Request) {
	var req scheduleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apiresp.Err(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if err := scheduler.ValidateSpec(req.Spec); err != nil {
		apiresp.Err(w, http.StatusBadRequest, "cron 表达式无效: "+err.Error())
		return
	}
	sc, err := h.Store.UpdateSchedule(chi.URLParam(r, "id"), req.toSchedule())
	if err != nil {
		h.respondStoreErr(w, err)
		return
	}
	h.Scheduler.Reload()
	apiresp.OK(w, sc)
}

// DeleteSchedule 删除定时任务。
func (h *Handlers) DeleteSchedule(w http.ResponseWriter, r *http.Request) {
	if err := h.Store.DeleteSchedule(chi.URLParam(r, "id")); err != nil {
		h.respondStoreErr(w, err)
		return
	}
	h.Scheduler.Reload()
	apiresp.OK(w, map[string]any{"deleted": true})
}

// ToggleSchedule 启用/停用定时任务。
func (h *Handlers) ToggleSchedule(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		apiresp.Err(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if err := h.Store.ToggleSchedule(chi.URLParam(r, "id"), body.Enabled); err != nil {
		h.respondStoreErr(w, err)
		return
	}
	h.Scheduler.Reload()
	apiresp.OK(w, map[string]any{"enabled": body.Enabled})
}

// RunSchedule 立即手动触发一个定时任务（进入队列）。
func (h *Handlers) RunSchedule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := h.Store.GetSchedule(id); !ok {
		apiresp.Err(w, http.StatusNotFound, "定时任务不存在")
		return
	}
	runID, ok := h.Scheduler.Trigger(id)
	if !ok {
		apiresp.Err(w, http.StatusConflict, "触发失败：该任务可能正在运行/排队，或 IP 源缺失")
		return
	}
	apiresp.OK(w, map[string]any{"run_id": runID})
}
