// Package store 负责所有持久化：测速历史记录、CDN Profile、对外 License 密钥与全局设置。
// 元数据(设置/Profile/License)存于单个 meta.json；每次测速记录存为 runs/<id>.json，
// 并维护一份 runs/index.json 摘要清单用于快速列表。
package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/mia-clark/cfst-manager/internal/engine"
)

// 三网线路标识。
const (
	LineCM = "CM" // 移动
	LineCU = "CU" // 联通
	LineCT = "CT" // 电信
)

// 内置 CDN Profile 名称。
var BuiltinProfiles = []string{"cloudflare", "cloudfront", "edgeone"}

// Run 是一次完整测速的持久化记录。
type Run struct {
	ID         string          `json:"id"`
	CreatedAt  time.Time       `json:"created_at"`
	FinishedAt time.Time       `json:"finished_at"`
	Status     string          `json:"status"` // finished|failed|stopped
	Error      string          `json:"error,omitempty"`
	Profile    string          `json:"profile"`    // 目标 CDN profile
	IPVersion  string          `json:"ip_version"` // v4|v6|mixed
	Trigger    string          `json:"trigger,omitempty"` // 触发来源(手动/定时:名称)
	Note       string          `json:"note,omitempty"`
	Config     engine.Config   `json:"config"`
	Results    []engine.Result `json:"results"`
}

// RunSummary 是历史列表用的轻量摘要(不含完整结果)。
type RunSummary struct {
	ID         string    `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	FinishedAt time.Time `json:"finished_at"`
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
	Profile    string    `json:"profile"`
	IPVersion  string    `json:"ip_version"`
	Note       string    `json:"note,omitempty"`
	Count      int       `json:"count"`    // 结果数量
	BestIP     string    `json:"best_ip"`  // 最优 IP
	BestMBps   float64   `json:"best_mbps"`// 最优下载速度
}

// Profile 是一个 CDN 的对外发布配置。CM/CU/CT 三线各自可绑定不同 run；
// 留空则回退到 PublishedV4/V6RunID。
type Profile struct {
	Name             string `json:"name"`
	Title            string `json:"title"`
	PublishedV4RunID string `json:"published_v4_run_id"`
	PublishedV6RunID string `json:"published_v6_run_id"`
	// 可选的三线覆盖(run id)。key: CM/CU/CT；空 map 表示三线统一用已发布 run。
	LineV4 map[string]string `json:"line_v4,omitempty"`
	LineV6 map[string]string `json:"line_v6,omitempty"`
}

// License 是对外 API 的授权密钥。
type License struct {
	Key       string    `json:"key"`
	Note      string    `json:"note"`
	Enabled   bool      `json:"enabled"`
	Count     int64     `json:"count"` // 积分余额(用于 license 端点回显)
	CreatedAt time.Time `json:"created_at"`
}

// Settings 是全局设置。
type Settings struct {
	PublicOpen      bool          `json:"public_open"`       // 公开模式:接受任意 key
	PublicResultMax int           `json:"public_result_max"` // 公开 API 单次返回 IP 数上限
	AutoPublish     bool          `json:"auto_publish"`      // 测速完成自动发布到对应 profile
	DefaultConfig   engine.Config `json:"default_config"`    // 测速默认参数(前端预填)
}

// meta 是 meta.json 的根结构。
type meta struct {
	Settings  Settings            `json:"settings"`
	Profiles  map[string]*Profile `json:"profiles"`
	Licenses  []License           `json:"licenses"`
	Schedules []Schedule          `json:"schedules"`
}

// Store 是持久化门面，并发安全。
type Store struct {
	mu       sync.RWMutex
	metaPath string
	runsDir  string
	meta     meta
	index    []RunSummary // 历史摘要，按时间倒序
}

// New 构造 Store。
func New(metaPath, runsDir string) *Store {
	return &Store{metaPath: metaPath, runsDir: runsDir}
}

// Load 从磁盘加载 meta 与历史索引；首启时填充默认值与内置 Profile。
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// meta.json
	freshInstall := true
	if b, err := os.ReadFile(s.metaPath); err == nil {
		freshInstall = false
		if err := json.Unmarshal(b, &s.meta); err != nil {
			return fmt.Errorf("解析 meta.json 失败: %w", err)
		}
	}
	if s.meta.Profiles == nil {
		s.meta.Profiles = map[string]*Profile{}
	}
	// 默认设置
	if s.meta.Settings.PublicResultMax <= 0 {
		s.meta.Settings.PublicResultMax = 10
	}
	if freshInstall { // 全新安装默认开启自动发布，使测速完成即可经公开 API 取用
		s.meta.Settings.AutoPublish = true
	}
	// 自动迁移已失效/不稳的默认测速地址 → 引擎当前默认（Cloudflare 官方 50MB）：
	//   ① cf.xiu2.xyz/url：旧 CFST 默认，限额 Worker，已 403/失真。
	//   ② speed.cloudflare.com __down bytes=200000000(200MB)：曾短暂作默认，实测部分线路
	//      (国内 IPv6→海外节点) ≥100MB 会被直接拒成 403，故一并迁到 50MB。
	switch s.meta.Settings.DefaultConfig.URL {
	case "https://cf.xiu2.xyz/url",
		"https://speed.cloudflare.com/__down?bytes=200000000":
		s.meta.Settings.DefaultConfig.URL = engine.DefaultURL
	}
	// 用引擎默认值补全测速默认参数，避免前端表单预填出现 0/空（Normalize 只填零值/非法值，不覆盖用户设定）。
	s.meta.Settings.DefaultConfig.Normalize()
	// 内置 Profile 兜底
	titles := map[string]string{"cloudflare": "Cloudflare", "cloudfront": "AWS CloudFront", "edgeone": "腾讯 EdgeOne"}
	for _, name := range BuiltinProfiles {
		if _, ok := s.meta.Profiles[name]; !ok {
			s.meta.Profiles[name] = &Profile{Name: name, Title: titles[name]}
		}
	}

	// 历史索引
	if err := s.loadIndex(); err != nil {
		return err
	}
	if err := s.persistMetaLocked(); err != nil {
		return err
	}
	return nil
}

func (s *Store) indexPath() string { return filepath.Join(s.runsDir, "index.json") }

// loadIndex 读取 index.json；若缺失则扫描 runs 目录重建。
func (s *Store) loadIndex() error {
	if b, err := os.ReadFile(s.indexPath()); err == nil {
		_ = json.Unmarshal(b, &s.index)
	}
	if s.index == nil {
		s.index = []RunSummary{}
		// 扫描重建
		entries, _ := os.ReadDir(s.runsDir)
		for _, e := range entries {
			if e.IsDir() || e.Name() == "index.json" || filepath.Ext(e.Name()) != ".json" {
				continue
			}
			b, err := os.ReadFile(filepath.Join(s.runsDir, e.Name()))
			if err != nil {
				continue
			}
			var run Run
			if json.Unmarshal(b, &run) == nil && run.ID != "" {
				s.index = append(s.index, summarize(run))
			}
		}
		s.sortIndexLocked()
		_ = s.persistIndexLocked()
	}
	return nil
}

func (s *Store) sortIndexLocked() {
	sort.Slice(s.index, func(i, j int) bool { return s.index[i].CreatedAt.After(s.index[j].CreatedAt) })
}

func (s *Store) persistMetaLocked() error {
	return writeJSONAtomic(s.metaPath, s.meta)
}

func (s *Store) persistIndexLocked() error {
	return writeJSONAtomic(s.indexPath(), s.index)
}

func (s *Store) runPath(id string) string { return filepath.Join(s.runsDir, id+".json") }

// NewRunID 生成一个时间有序且唯一的 run id。
func NewRunID(now time.Time) string {
	buf := make([]byte, 3)
	_, _ = rand.Read(buf)
	return now.Format("20060102-150405") + "-" + hex.EncodeToString(buf)
}

// summarize 从完整 Run 提取摘要。
func summarize(r Run) RunSummary {
	sum := RunSummary{
		ID: r.ID, CreatedAt: r.CreatedAt, FinishedAt: r.FinishedAt,
		Status: r.Status, Error: r.Error, Profile: r.Profile,
		IPVersion: r.IPVersion, Note: r.Note, Count: len(r.Results),
	}
	if len(r.Results) > 0 {
		// 结果已按速度(或延迟)排序，取首个为最优
		sum.BestIP = r.Results[0].IP
		sum.BestMBps = r.Results[0].SpeedMBps
	}
	return sum
}

// writeJSONAtomic 原子写 JSON（先写临时文件再 rename）。
func writeJSONAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp) // 重命名失败时清理临时文件，避免残留
		return err
	}
	return nil
}
