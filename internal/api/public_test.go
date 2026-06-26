package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/nue-mic/cfst-manager/internal/appcfg"
	"github.com/nue-mic/cfst-manager/internal/engine"
	"github.com/nue-mic/cfst-manager/internal/store"
)

func setupHandlers(t *testing.T) (*Handlers, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	runsDir := filepath.Join(dir, "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st := store.New(filepath.Join(dir, "meta.json"), runsDir)
	if err := st.Load(); err != nil {
		t.Fatal(err)
	}
	h := &Handlers{Deps{Cfg: &appcfg.Config{PublicOpen: false}, Store: st}}
	return h, st
}

func doReq(t *testing.T, h *Handlers, path string) string {
	t.Helper()
	r := chi.NewRouter()
	h.mountPublic(r)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return strings.TrimSpace(w.Body.String())
}

// TestPublicIPCompat 校验对外优选 IP API 与 wetest.vip 原始字节逐字段一致。
func TestPublicIPCompat(t *testing.T) {
	h, st := setupHandlers(t)

	// 缺参
	if got := doReq(t, h, "/api/cf2dns/get_cloudflare_ip"); got != `{"status":false,"code":500,"msg":"未提交key、type参数","info":""}` {
		t.Fatalf("缺参响应不一致:\n%s", got)
	}

	// 无效 key（非公开模式）
	want := `{"status":true,"code":500,"msg":"KEY不存在","info":{"CM":[],"CU":[],"CT":[]}}`
	if got := doReq(t, h, "/api/cf2dns/get_cloudflare_ip?key=bad&type=v4"); got != want {
		t.Fatalf("无效key响应不一致:\n got=%s\nwant=%s", got, want)
	}

	// 登记 key + 发布一条 v4 结果
	if _, err := st.AddLicense("testkey", "", 0); err != nil {
		t.Fatal(err)
	}
	run := store.Run{
		ID: "20260101-000000-aaa", CreatedAt: time.Now(), FinishedAt: time.Now(),
		Status: "finished", Profile: "cloudflare",
		Results: []engine.Result{{IP: "104.16.1.1", Colo: "SJC", DelayMS: 150.5, Sended: 4, Received: 4}},
	}
	if err := st.SaveRun(run); err != nil { // 自动发布(fresh install 默认开启)
		t.Fatal(err)
	}

	// 有效 key → 应返回真实数据
	body := doReq(t, h, "/api/cf2dns/get_cloudflare_ip?key=testkey&type=v4")
	var resp struct {
		Status bool   `json:"status"`
		Code   int    `json:"code"`
		Msg    string `json:"msg"`
		Info   struct {
			CM []struct {
				IP      string  `json:"ip"`
				Colo    string  `json:"colo"`
				Latency float64 `json:"latency"`
			} `json:"CM"`
			CU []any `json:"CU"`
			CT []any `json:"CT"`
		} `json:"info"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("成功响应解析失败: %v\n%s", err, body)
	}
	if !resp.Status || resp.Code != 200 || resp.Msg != "请求成功" {
		t.Fatalf("成功信封不一致: %s", body)
	}
	if len(resp.Info.CM) != 1 || resp.Info.CM[0].IP != "104.16.1.1" || resp.Info.CM[0].Colo != "SJC" || resp.Info.CM[0].Latency != 150.5 {
		t.Fatalf("CM 数据不一致: %s", body)
	}

	// 字段顺序必须是 status,code,msg,info（与 wetest 一致）
	if i := strings.Index(body, `"status"`); i != 0 && !strings.HasPrefix(body, `{"status"`) {
		t.Fatalf("字段顺序应以 status 开头: %s", body)
	}
}

// TestPublicLicenseCompat 校验 License 端点。
func TestPublicLicenseCompat(t *testing.T) {
	h, st := setupHandlers(t)
	if _, err := st.AddLicense("testkey", "", 0); err != nil {
		t.Fatal(err)
	}

	if got := doReq(t, h, "/api/cf2dns/get_cloudflare_license?license=bad"); got != `{"status":false,"code":500,"msg":"License未找到","info":""}` {
		t.Fatalf("无效 license 响应不一致:\n%s", got)
	}
	got := doReq(t, h, "/api/cf2dns/get_cloudflare_license?license=testkey")
	want := `{"status":true,"code":200,"count":99999999,"token":"testkey","info":"Request success!"}`
	if got != want {
		t.Fatalf("有效 license 响应不一致:\n got=%s\nwant=%s", got, want)
	}
}

// TestPublicOpenMode 校验公开模式放行任意 key。
func TestPublicOpenMode(t *testing.T) {
	h, _ := setupHandlers(t)
	h.Cfg.PublicOpen = true
	body := doReq(t, h, "/api/cf2dns/get_cloudflare_ip?key=whatever&type=v4")
	if !strings.HasPrefix(body, `{"status":true,"code":200,"msg":"请求成功"`) {
		t.Fatalf("公开模式应放行任意 key: %s", body)
	}
}
