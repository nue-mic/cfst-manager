package store

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nue-mic/cfst-manager/internal/engine"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	runsDir := filepath.Join(dir, "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st := New(filepath.Join(dir, "meta.json"), runsDir)
	if err := st.Load(); err != nil {
		t.Fatal(err)
	}
	return st
}

func sampleRun(id, profile string, results []engine.Result) Run {
	return Run{
		ID: id, CreatedAt: time.Now(), FinishedAt: time.Now(),
		Status: "finished", Profile: profile, Results: results,
	}
}

func TestSaveListGetDeleteRun(t *testing.T) {
	st := newTestStore(t)
	r := sampleRun("20260101-000000-aaa", "cloudflare", []engine.Result{{IP: "1.1.1.1", DelayMS: 10, SpeedMBps: 5}})
	if err := st.SaveRun(r); err != nil {
		t.Fatal(err)
	}
	if got := st.ListRuns(""); len(got) != 1 || got[0].BestIP != "1.1.1.1" {
		t.Fatalf("ListRuns 错误: %+v", got)
	}
	full, err := st.GetRun(r.ID)
	if err != nil || len(full.Results) != 1 {
		t.Fatalf("GetRun 错误: %v %+v", err, full)
	}
	if err := st.DeleteRun(r.ID); err != nil {
		t.Fatal(err)
	}
	if got := st.ListRuns(""); len(got) != 0 {
		t.Fatalf("删除后仍有记录: %+v", got)
	}
}

func TestPublishAndGetLines(t *testing.T) {
	st := newTestStore(t)
	r := sampleRun("20260101-000000-bbb", "cloudflare", []engine.Result{
		{IP: "104.16.1.1", Colo: "SJC", DelayMS: 120, SpeedMBps: 9},
		{IP: "2606:4700::1", Colo: "LAX", DelayMS: 80},
	})
	r.IPVersion = DetectIPVersion(r.Results) // mixed
	if err := st.SaveRun(r); err != nil {    // 自动发布 mixed → v4+v6
		t.Fatal(err)
	}
	lines := st.GetPublishedLines("cloudflare", "v4")
	if len(lines[LineCM]) != 1 || lines[LineCM][0].IP != "104.16.1.1" {
		t.Fatalf("v4 线路数据错误: %+v", lines)
	}
	linesV6 := st.GetPublishedLines("cloudflare", "v6")
	if len(linesV6[LineCT]) != 1 || linesV6[LineCT][0].IP != "2606:4700::1" {
		t.Fatalf("v6 线路数据错误: %+v", linesV6)
	}
}

func TestLicenseValidate(t *testing.T) {
	st := newTestStore(t)
	if _, ok := st.ValidateLicense("nope"); ok {
		t.Fatal("未登记的 key 不应通过")
	}
	lic, _ := st.AddLicense("k1", "test", 0)
	if lic.Count != 99999999 {
		t.Fatalf("默认余额错误: %d", lic.Count)
	}
	if _, ok := st.ValidateLicense("k1"); !ok {
		t.Fatal("已登记的 key 应通过")
	}
	_ = st.UpdateLicense("k1", "x", false, 0)
	if _, ok := st.ValidateLicense("k1"); ok {
		t.Fatal("停用的 key 不应通过")
	}
}

// TestCloneProfileIndependent 验证对外返回的 Profile 与内部 map 隔离（修复关键 critical 竞态的机制）。
func TestCloneProfileIndependent(t *testing.T) {
	st := newTestStore(t)
	_ = st.UpsertProfile(Profile{Name: "cloudflare", Title: "CF", LineV4: map[string]string{"CM": "run1"}})
	p, ok := st.GetProfile("cloudflare")
	if !ok || p.LineV4["CM"] != "run1" {
		t.Fatalf("GetProfile 错误: %+v", p)
	}
	// 修改返回副本的 map，不应影响 store 内部
	p.LineV4["CM"] = "HACKED"
	p.LineV4["CU"] = "INJECT"
	again, _ := st.GetProfile("cloudflare")
	if again.LineV4["CM"] != "run1" || len(again.LineV4) != 1 {
		t.Fatalf("Profile map 未隔离，存在共享: %+v", again.LineV4)
	}
}

// TestConcurrentProfileAccess 并发压测：读 map(GetPublishedLines/ListProfiles) 与写 map(UpsertProfile/DeleteRun)
// 同时进行。修复前共享 map 头会触发 Go 运行时致命的 "concurrent map read and map write"；修复后应平稳完成。
func TestConcurrentProfileAccess(t *testing.T) {
	st := newTestStore(t)
	_ = st.UpsertProfile(Profile{Name: "cloudflare", LineV4: map[string]string{"CM": "r", "CU": "r", "CT": "r"}, PublishedV4RunID: "r"})
	_ = st.SaveRun(sampleRun("r", "cloudflare", []engine.Result{{IP: "1.1.1.1", DelayMS: 1}}))

	var wg sync.WaitGroup
	const iters = 300
	// 读者
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				_ = st.GetPublishedLines("cloudflare", "v4")
				_ = st.ListProfiles()
				_, _ = st.GetProfile("cloudflare")
			}
		}()
	}
	// 写者：反复用带 map 的 Profile 覆盖
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				_ = st.UpsertProfile(Profile{Name: "cloudflare", LineV4: map[string]string{"CM": "r", "CU": "r", "CT": "r"}, PublishedV4RunID: "r"})
			}
		}()
	}
	wg.Wait()
}
