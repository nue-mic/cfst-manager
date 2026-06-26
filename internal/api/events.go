package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/nue-mic/cfst-manager/internal/eventbus"
)

// Events 是 SSE 端点：把测速进度/状态/日志实时推给浏览器 EventSource。
func (h *Handlers) Events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "服务端不支持流式响应", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // 禁用 Nginx 缓冲
	w.WriteHeader(http.StatusOK)

	write := func(e eventbus.Event) bool {
		b, err := json.Marshal(e)
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, b); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	// 连接建立时先补发最近一次状态快照 + 当前运行状态，便于前端立即渲染。
	if snap := h.Bus.LastSnapshot(); snap != nil {
		if !write(*snap) {
			return
		}
	}
	statusEvent := eventbus.Event{
		Type: "status", TS: time.Now().UTC(), Data: h.Runner.Status(),
	}
	if !write(statusEvent) {
		return
	}

	sub := h.Bus.Subscribe(256)
	defer sub.Unsubscribe()

	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-sub.C():
			if !ok {
				return
			}
			if !write(e) {
				return
			}
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
