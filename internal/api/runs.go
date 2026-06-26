package api

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/nue-mic/cfst-manager/internal/api/apiresp"
	"github.com/nue-mic/cfst-manager/internal/store"
)

// ListRuns 列出历史测速摘要。?profile= 可过滤。
func (h *Handlers) ListRuns(w http.ResponseWriter, r *http.Request) {
	apiresp.OK(w, h.Store.ListRuns(r.URL.Query().Get("profile")))
}

// GetRun 返回完整测速记录。
func (h *Handlers) GetRun(w http.ResponseWriter, r *http.Request) {
	run, err := h.Store.GetRun(chi.URLParam(r, "id"))
	if err != nil {
		h.respondStoreErr(w, err)
		return
	}
	apiresp.OK(w, run)
}

// DeleteRun 删除测速记录。
func (h *Handlers) DeleteRun(w http.ResponseWriter, r *http.Request) {
	if err := h.Store.DeleteRun(chi.URLParam(r, "id")); err != nil {
		h.respondStoreErr(w, err)
		return
	}
	apiresp.OK(w, map[string]any{"deleted": true})
}

// ExportRun 导出结果。?format=json|txt|csv（默认 csv）；?n= 限制数量。
func (h *Handlers) ExportRun(w http.ResponseWriter, r *http.Request) {
	run, err := h.Store.GetRun(chi.URLParam(r, "id"))
	if err != nil {
		h.respondStoreErr(w, err)
		return
	}
	results := run.Results
	if n, e := strconv.Atoi(r.URL.Query().Get("n")); e == nil && n > 0 && n < len(results) {
		results = results[:n]
	}

	switch strings.ToLower(r.URL.Query().Get("format")) {
	case "json":
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+run.ID+".json\"")
		_ = json.NewEncoder(w).Encode(results)
	case "txt":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+run.ID+".txt\"")
		var b strings.Builder
		for _, it := range results {
			b.WriteString(it.IP)
			b.WriteByte('\n')
		}
		_, _ = w.Write([]byte(b.String()))
	default: // csv（与 CFST result.csv 同构）
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+run.ID+".csv\"")
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"IP 地址", "已发送", "已接收", "丢包率", "平均延迟", "下载速度(MB/s)", "地区码"})
		for _, it := range results {
			_ = cw.Write([]string{
				it.IP,
				strconv.Itoa(it.Sended),
				strconv.Itoa(it.Received),
				fmt.Sprintf("%.2f", it.LossRate),
				fmt.Sprintf("%.2f", it.DelayMS),
				fmt.Sprintf("%.2f", it.SpeedMBps),
				it.Colo,
			})
		}
		cw.Flush()
	}
}

// publishReq 是手动发布请求体。
type publishReq struct {
	Profile   string `json:"profile"`
	IPVersion string `json:"ip_version"`
}

// PublishRun 把某条记录手动发布到指定 Profile 的对应版本槽位。
func (h *Handlers) PublishRun(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	run, err := h.Store.GetRun(id)
	if err != nil {
		h.respondStoreErr(w, err)
		return
	}
	var req publishReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	profile := req.Profile
	if profile == "" {
		profile = run.Profile
	}
	ipv := req.IPVersion
	if ipv == "" {
		ipv = run.IPVersion
	}
	// 防假成功：版本无法确定(如空结果记录)时 publishLocked 会静默 no-op，这里直接拒绝。
	if ipv != "v4" && ipv != "v6" && ipv != "mixed" {
		apiresp.Err(w, http.StatusBadRequest, "无法确定该记录的 IP 版本，请显式指定 v4/v6/mixed（空结果记录不可发布）")
		return
	}
	if err := h.Store.Publish(profile, ipv, id); err != nil {
		apiresp.Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	apiresp.OK(w, map[string]any{"published": true, "profile": profile, "ip_version": ipv})
}

func (h *Handlers) respondStoreErr(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		apiresp.Err(w, http.StatusNotFound, err.Error())
		return
	}
	apiresp.Err(w, http.StatusInternalServerError, err.Error())
}
