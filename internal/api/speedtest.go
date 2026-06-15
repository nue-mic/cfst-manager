package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/mia-clark/cfst-manager/internal/api/apiresp"
	"github.com/mia-clark/cfst-manager/internal/engine"
	"github.com/mia-clark/cfst-manager/internal/runner"
	"github.com/mia-clark/cfst-manager/internal/store"
)

// startReq 是开始测速的请求体：内嵌全部引擎参数 + 目标 profile + 备注 + IP 源选择。
type startReq struct {
	engine.Config
	Profile  string `json:"profile"`   // 目标 CDN profile（cloudflare/cloudfront/edgeone/...）
	Note     string `json:"note"`      // 备注
	IPSource string `json:"ip_source"` // 选用的 IP 源文件名（与 ip_text 二选一，ip_text 优先）
}

// StartSpeedtest 开始一次测速。
func (h *Handlers) StartSpeedtest(w http.ResponseWriter, r *http.Request) {
	var req startReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		apiresp.Err(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}

	cfg := req.Config
	profile := strings.TrimSpace(req.Profile)
	if profile == "" {
		profile = "cloudflare"
	}

	// 解析 IP 来源：ip_text 优先，其次 ip_source 文件，再次已填的 ip_file，最后默认 ip.txt。
	if strings.TrimSpace(cfg.IPText) == "" {
		name := strings.TrimSpace(req.IPSource)
		if name == "" && strings.TrimSpace(cfg.IPFile) == "" {
			name = "ip.txt"
		}
		if name != "" {
			path, err := h.Sources.Path(name)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					apiresp.Err(w, http.StatusBadRequest, "IP 源不存在: "+name)
					return
				}
				apiresp.Err(w, http.StatusBadRequest, err.Error())
				return
			}
			cfg.IPFile = path
		}
	}

	// 手动任务进入统一串行队列；若有任务在跑则自动排队（ahead>0）。
	// 发布依赖全局自动发布设置，故此处 Publish=false。
	runID, ahead := h.Runner.Submit(runner.SubmitOpts{
		Config: cfg, Profile: profile, Note: req.Note, Trigger: "手动", Publish: false,
	})
	apiresp.OK(w, map[string]any{"run_id": runID, "ahead": ahead, "queued": ahead > 0})
}

// StopSpeedtest 中止当前正在运行的测速（队列后续任务继续）。
func (h *Handlers) StopSpeedtest(w http.ResponseWriter, r *http.Request) {
	if !h.Runner.Stop() {
		apiresp.Err(w, http.StatusConflict, "当前没有正在运行的测速任务")
		return
	}
	apiresp.OK(w, map[string]any{"stopped": true})
}

// SpeedtestStatus 返回当前测速状态（含队列）。
func (h *Handlers) SpeedtestStatus(w http.ResponseWriter, r *http.Request) {
	apiresp.OK(w, h.Runner.Status())
}

// CancelQueued 取消队列中尚未开始的某个任务。
func (h *Handlers) CancelQueued(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !h.Runner.CancelQueued(id) {
		apiresp.Err(w, http.StatusNotFound, "队列中没有该任务（可能已开始或已完成）")
		return
	}
	apiresp.OK(w, map[string]any{"canceled": id})
}

// ClearQueue 清空全部排队中的任务。
func (h *Handlers) ClearQueue(w http.ResponseWriter, r *http.Request) {
	n := h.Runner.ClearQueue()
	apiresp.OK(w, map[string]any{"cleared": n})
}
