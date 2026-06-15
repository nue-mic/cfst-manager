package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/mia-clark/cfst-manager/internal/engine"
	"github.com/mia-clark/cfst-manager/internal/store"
)

// ============================================================================
// 对外兼容 API —— 逐字段对齐 wetest.vip(微测网) 与 api.hostmonit.com，
// 使已部署的第三方客户端(如 ddgth/cf2dns)可零改动无缝迁移到本服务。
//
// 契约(实测自 wetest.vip 原始字节 + cf2dns 源码验证):
//   GET|POST /api/cf2dns/get_<cdn>_ip?key=K&type=v4|v6
//     成功: {"status":true,"code":200,"msg":"请求成功",
//            "info":{"CM":[{"ip","colo","latency"}],"CU":[...],"CT":[...]}}
//     缺参: {"status":false,"code":500,"msg":"未提交key、type参数","info":""}
//     无效key: {"status":true,"code":500,"msg":"KEY不存在","info":{"CM":[],"CU":[],"CT":[]}}
//   GET|POST /api/cf2dns/get_<cdn>_license?license=L
//     成功: {"status":true,"code":200,"count":N,"token":"L","info":"Request success!"}
//     无效: {"status":false,"code":500,"msg":"License未找到","info":""}
//   POST /get_optimization_ip  (hostmonit 兼容, JSON body {"key","type"})
// ============================================================================

// pubIP 是对外返回的单个优选 IP 对象。
type pubIP struct {
	IP      string  `json:"ip"`
	Colo    string  `json:"colo"`
	Latency float64 `json:"latency"`
}

// ipInfo 是按三网分类的优选结果。
type ipInfo struct {
	CM []pubIP `json:"CM"`
	CU []pubIP `json:"CU"`
	CT []pubIP `json:"CT"`
}

// ipResp / licResp 用显式结构体锁定字段顺序，确保与 wetest 原始响应逐字节同构
// （map 会按 key 字母序输出，破坏字段顺序）。
type ipResp struct {
	Status bool   `json:"status"`
	Code   int    `json:"code"`
	Msg    string `json:"msg"`
	Info   any    `json:"info"` // 成功/无效key 为 ipInfo；缺参为 ""
}

type licResp struct {
	Status bool   `json:"status"`
	Code   int    `json:"code"`
	Count  int64  `json:"count,omitempty"` // 仅成功时
	Token  string `json:"token,omitempty"` // 仅成功时
	Msg    string `json:"msg,omitempty"`   // 仅错误时
	Info   string `json:"info"`
}

// mountPublic 注册全部对外兼容端点（GET/POST 均可，无需 Bearer 鉴权）。
func (h *Handlers) mountPublic(r chi.Router) {
	for _, p := range store.BuiltinProfiles {
		profile := p
		r.HandleFunc("/api/cf2dns/get_"+profile+"_ip", h.publicIP(profile))
		r.HandleFunc("/api/cf2dns/get_"+profile+"_license", h.publicLicense)
	}
	// hostmonit 兼容端点（默认按 cloudflare profile 取数）
	r.HandleFunc("/get_optimization_ip", h.publicIP("cloudflare"))
}

// publicIP 返回某 CDN profile 的优选 IP（三网分类），结构与 wetest 完全一致。
func (h *Handlers) publicIP(profile string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		params := extractParams(r)
		key := strings.TrimSpace(params["key"])
		typ := strings.TrimSpace(strings.ToLower(params["type"]))

		// 缺参或 type 非法：与 wetest 一致，统一归入同一条参数错误响应
		// （wetest 不存在单独的 type 错误分支）。
		if key == "" || (typ != "v4" && typ != "v6") {
			writeJSON(w, ipResp{Status: false, Code: 500, Msg: "未提交key、type参数", Info: ""})
			return
		}

		// 校验授权 key（开放模式下任意 key 通过；env CFST_PUBLIC_OPEN 为强制开放覆盖）。
		if !h.publicOpen() {
			if _, ok := h.Store.ValidateLicense(key); !ok {
				writeJSON(w, ipResp{
					Status: true, Code: 500, Msg: "KEY不存在",
					Info: ipInfo{CM: []pubIP{}, CU: []pubIP{}, CT: []pubIP{}},
				})
				return
			}
		}

		max := h.Store.GetSettings().PublicResultMax
		lines := h.Store.GetPublishedLines(profile, typ)
		info := ipInfo{
			CM: toPubIPs(lines[store.LineCM], max),
			CU: toPubIPs(lines[store.LineCU], max),
			CT: toPubIPs(lines[store.LineCT], max),
		}
		writeJSON(w, ipResp{Status: true, Code: 200, Msg: "请求成功", Info: info})
	}
}

// publicOpen 报告公开模式是否开启：env 强制 或 UI 设置。
func (h *Handlers) publicOpen() bool {
	return h.Cfg.PublicOpen || h.Store.GetSettings().PublicOpen
}

// publicLicense 返回授权信息，结构与 wetest 完全一致。
func (h *Handlers) publicLicense(w http.ResponseWriter, r *http.Request) {
	params := extractParams(r)
	// wetest 该端点参数名为 license；为宽容也接受 key。
	key := strings.TrimSpace(params["license"])
	if key == "" {
		key = strings.TrimSpace(params["key"])
	}
	lic, ok := h.Store.ValidateLicense(key)
	if !ok && h.Cfg.PublicOpen && key != "" {
		ok = true
		lic = store.License{Key: key, Count: 99999999}
	}
	if !ok {
		writeJSON(w, licResp{Status: false, Code: 500, Msg: "License未找到", Info: ""})
		return
	}
	count := lic.Count
	if count <= 0 {
		count = 99999999
	}
	writeJSON(w, licResp{Status: true, Code: 200, Count: count, Token: key, Info: "Request success!"})
}

// toPubIPs 把引擎结果转换为对外 IP 列表并限制数量。
func toPubIPs(results []engine.Result, max int) []pubIP {
	if max <= 0 {
		max = 10
	}
	out := make([]pubIP, 0, max)
	for _, r := range results {
		if len(out) >= max {
			break
		}
		colo := r.Colo
		if colo == "N/A" {
			colo = ""
		}
		out = append(out, pubIP{
			IP:      r.IP,
			Colo:    colo,
			Latency: round1(r.DelayMS),
		})
	}
	return out
}

func round1(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}

// extractParams 从请求中提取参数，兼容三种来源：URL 查询、表单(x-www-form-urlencoded)、JSON body。
func extractParams(r *http.Request) map[string]string {
	vals := map[string]string{}
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			vals[k] = v[0]
		}
	}
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		// 这些是无需鉴权的公开端点，限制请求体大小以防内存型 DoS。
		const maxBody = 256 << 10 // 256 KiB
		ct := r.Header.Get("Content-Type")
		if strings.Contains(ct, "application/json") {
			dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
			dec.UseNumber() // 数字保持原样字符串，避免大整数被转成科学计数法
			var m map[string]any
			if dec.Decode(&m) == nil {
				for k, v := range m {
					vals[k] = fmt.Sprintf("%v", v)
				}
			}
		} else {
			r.Body = io.NopCloser(io.LimitReader(r.Body, maxBody))
			if r.ParseForm() == nil {
				for k, v := range r.PostForm {
					if len(v) > 0 {
						vals[k] = v[0]
					}
				}
			}
		}
	}
	return vals
}

// writeJSON 写出紧凑 JSON（对外 API 不缩进，贴近原服务）。
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}
