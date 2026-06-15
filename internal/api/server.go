// Package api 装配 HTTP 路由：管理端 REST API、SSE 事件流、对外兼容 API 与内嵌 Web UI。
package api

import (
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/mia-clark/cfst-manager/internal/api/middleware"
	"github.com/mia-clark/cfst-manager/internal/appcfg"
	"github.com/mia-clark/cfst-manager/internal/eventbus"
	"github.com/mia-clark/cfst-manager/internal/runner"
	"github.com/mia-clark/cfst-manager/internal/scheduler"
	"github.com/mia-clark/cfst-manager/internal/store"
	"github.com/mia-clark/cfst-manager/web"
)

// Deps 汇集处理器所需的协作者。
type Deps struct {
	Cfg       *appcfg.Config
	Logger    *slog.Logger
	Store     *store.Store
	Sources   *store.SourceManager
	Runner    *runner.Runner
	Scheduler *scheduler.Scheduler
	Bus       *eventbus.Bus
	Version   string
}

// Handlers 持有全部依赖，方法分散在各 handler 文件中。
type Handlers struct {
	Deps
}

// NewRouter 组装 chi 路由。
func NewRouter(d Deps) http.Handler {
	h := &Handlers{Deps: d}
	r := chi.NewRouter()

	r.Use(middleware.Recover(d.Logger))
	r.Use(middleware.AccessLog(d.Logger))
	r.Use(middleware.CORS(d.Cfg.CORSOrigins))

	// ---- 无需鉴权 ----
	r.Get("/api/v1/health", h.Health)
	// 对外兼容 API（自带 key/license 校验，供第三方/已有客户端无缝接入）
	h.mountPublic(r)

	// ---- 管理端（Bearer 鉴权）----
	r.Group(func(r chi.Router) {
		r.Use(middleware.Bearer(d.Cfg.APIToken))

		r.Get("/api/v1/version", h.Version)

		// 测速控制 + 队列
		r.Post("/api/v1/speedtest/start", h.StartSpeedtest)
		r.Post("/api/v1/speedtest/stop", h.StopSpeedtest)
		r.Get("/api/v1/speedtest/status", h.SpeedtestStatus)
		r.Delete("/api/v1/speedtest/queue", h.ClearQueue)
		r.Delete("/api/v1/speedtest/queue/{id}", h.CancelQueued)

		// 定时任务
		r.Get("/api/v1/schedules", h.ListSchedules)
		r.Post("/api/v1/schedules", h.CreateSchedule)
		r.Get("/api/v1/schedules/{id}", h.GetSchedule)
		r.Put("/api/v1/schedules/{id}", h.UpdateSchedule)
		r.Delete("/api/v1/schedules/{id}", h.DeleteSchedule)
		r.Post("/api/v1/schedules/{id}/toggle", h.ToggleSchedule)
		r.Post("/api/v1/schedules/{id}/run", h.RunSchedule)

		// 历史记录
		r.Get("/api/v1/runs", h.ListRuns)
		r.Get("/api/v1/runs/{id}", h.GetRun)
		r.Delete("/api/v1/runs/{id}", h.DeleteRun)
		r.Get("/api/v1/runs/{id}/export", h.ExportRun)
		r.Post("/api/v1/runs/{id}/publish", h.PublishRun)

		// CDN Profile
		r.Get("/api/v1/profiles", h.ListProfiles)
		r.Get("/api/v1/profiles/{name}", h.GetProfile)
		r.Put("/api/v1/profiles/{name}", h.UpsertProfile)

		// 对外授权密钥
		r.Get("/api/v1/licenses", h.ListLicenses)
		r.Post("/api/v1/licenses", h.CreateLicense)
		r.Put("/api/v1/licenses/{key}", h.UpdateLicense)
		r.Delete("/api/v1/licenses/{key}", h.DeleteLicense)

		// IP 源
		r.Get("/api/v1/ipsources", h.ListIPSources)
		r.Get("/api/v1/ipsources/{name}", h.ReadIPSource)
		r.Put("/api/v1/ipsources/{name}", h.WriteIPSource)
		r.Delete("/api/v1/ipsources/{name}", h.DeleteIPSource)

		// 设置
		r.Get("/api/v1/settings", h.GetSettings)
		r.Put("/api/v1/settings", h.UpdateSettings)

		// SSE 事件流（EventSource 用 ?access_token= 携带令牌）
		r.Get("/api/v1/events", h.Events)
	})

	// ---- 内嵌 Web UI（SPA）----
	webFS := web.GetFS()
	fileServer := http.FileServer(http.FS(webFS))
	r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		filePath := strings.TrimPrefix(r.URL.Path, "/")
		if filePath != "" && filePath != "index.html" {
			if f, err := webFS.Open(filePath); err == nil {
				_ = f.Close()
				// 前端资源随二进制内嵌、版本随构建变化，禁用强缓存以便升级/调试即时生效。
				w.Header().Set("Cache-Control", "no-cache")
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(webFS, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(index)
	})

	return r
}
