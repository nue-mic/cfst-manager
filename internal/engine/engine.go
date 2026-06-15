// Package engine 是从 XIU2/CloudflareSpeedTest (GPL-3.0) 移植并改造的 CDN 优选 IP
// 测速核心。详见同目录 NOTICE.md。
//
// 与原项目相比，本包去掉了全部包级全局变量，改为通过 Config 结构体传参，
// 通过 ProgressFunc 回调上报进度，支持 context 取消，并以 error 取代 log.Fatal，
// 使其可安全地嵌入长期运行的 Web 守护进程。
package engine

import (
	"context"
	"fmt"
	"time"
)

// 默认参数（与 CFST 命令行默认值保持一致）。
const (
	DefaultRoutines     = 200
	MaxRoutines         = 1000
	DefaultPingTimes    = 4
	DefaultTestCount    = 10
	DefaultDownloadTime = 10 // 秒
	DefaultTCPPort      = 443
	// Cloudflare 官方测速端点：本身托管在 Cloudflare 上，强制连到候选 IP 即可测出
	// 该边缘真实下载速度。原 CFST 默认的 cf.xiu2.xyz/url 是共享占位地址（常 403/限速）。
	// 实测：bytes>=100MB 在部分线路(如国内 IPv6→海外节点)会被 Cloudflare 直接拒成 403，
	// 而 50MB 稳定可用，故默认用 50MB（高带宽服务器可在 UI 下拉改 100/200MB）。
	DefaultURL = "https://speed.cloudflare.com/__down?bytes=50000000"
	DefaultMaxDelay     = 9999 // ms
	DefaultMinDelay     = 0    // ms
	DefaultMaxLossRate  = 1.0
	DefaultMinSpeed     = 0.0
	DefaultIPFile       = "ip.txt"

	tcpConnectTimeout = time.Second * 1
	httpTimeout       = time.Second * 2
	downloadBufSize   = 1024
)

// Config 汇集全部可调测速参数，逐项对应 CFST 的命令行开关。
type Config struct {
	// 延迟测速
	Routines          int    `json:"routines"`            // -n   延迟测速线程 (默认 200, 最多 1000)
	PingTimes         int    `json:"ping_times"`          // -t   单 IP 延迟测速次数 (默认 4)
	TCPPort           int    `json:"tcp_port"`            // -tp  测速端口 (默认 443)
	Httping           bool   `json:"httping"`             // -httping       切换为 HTTP 协议测延迟
	HttpingStatusCode int    `json:"httping_status_code"` // -httping-code  HTTPing 有效状态码
	HttpingCFColo     string `json:"httping_cf_colo"`     // -cfcolo        匹配指定地区(机场码,逗号分隔)

	// 下载测速
	TestCount    int     `json:"test_count"`    // -dn  下载测速数量 (默认 10)
	DownloadTime int     `json:"download_time"` // -dt  单 IP 下载测速最长秒数 (默认 10)
	URL          string  `json:"url"`           // -url 测速地址
	MinSpeed     float64 `json:"min_speed"`     // -sl  下载速度下限 MB/s (默认 0)
	Disable      bool    `json:"disable"`       // -dd  禁用下载测速(仅延迟排序)

	// 结果过滤
	MaxDelay    int     `json:"max_delay"`     // -tl   平均延迟上限 ms (默认 9999)
	MinDelay    int     `json:"min_delay"`     // -tll  平均延迟下限 ms (默认 0)
	MaxLossRate float64 `json:"max_loss_rate"` // -tlr  丢包率上限 0~1 (默认 1)

	// IP 来源（IPText 优先于 IPFile）
	IPText  string `json:"ip_text"`  // -ip    直接指定 IP 段(逗号分隔)
	IPFile  string `json:"ip_file"`  // -f     IP 段数据文件路径
	TestAll bool   `json:"test_all"` // -allip 测速 IP 段内全部 IP(仅 IPv4)

	// 调试
	Debug bool `json:"debug"` // -debug 调试输出模式（出现非预期情况时输出更多诊断日志，逐字移植自上游）
}

// Normalize 用默认值补全非法/缺省字段，使引擎调用方无需逐项校验。
func (c *Config) Normalize() {
	if c.Routines <= 0 {
		c.Routines = DefaultRoutines
	}
	if c.Routines > MaxRoutines {
		c.Routines = MaxRoutines
	}
	if c.PingTimes <= 0 {
		c.PingTimes = DefaultPingTimes
	}
	if c.TCPPort <= 0 || c.TCPPort >= 65535 {
		c.TCPPort = DefaultTCPPort
	}
	if c.TestCount <= 0 {
		c.TestCount = DefaultTestCount
	}
	if c.DownloadTime <= 0 {
		c.DownloadTime = DefaultDownloadTime
	}
	if c.URL == "" {
		c.URL = DefaultURL
	}
	if c.MinSpeed < 0 {
		c.MinSpeed = 0
	}
	if c.MaxDelay <= 0 {
		c.MaxDelay = DefaultMaxDelay
	}
	if c.MinDelay < 0 {
		c.MinDelay = 0
	}
	if c.MaxLossRate < 0 {
		c.MaxLossRate = 0
	}
	if c.MaxLossRate > 1 {
		c.MaxLossRate = 1
	}
}

// Result 是单个 IP 的测速结果。JSON 标签用于历史记录持久化与管理 API。
type Result struct {
	IP        string  `json:"ip"`         // IP 地址
	Sended    int     `json:"sended"`     // 已发送探测数
	Received  int     `json:"received"`   // 已接收(成功)数
	LossRate  float64 `json:"loss_rate"`  // 丢包率 0~1
	DelayMS   float64 `json:"delay_ms"`   // 平均延迟(毫秒)
	SpeedMBps float64 `json:"speed_mbps"` // 下载速度(MB/s)
	Colo      string  `json:"colo"`       // 数据中心地区码(机场三字码)
}

// Stage 标识测速所处阶段。
type Stage string

const (
	StageLatency  Stage = "latency"  // 延迟测速
	StageDownload Stage = "download" // 下载测速
)

// Progress 是一次进度上报。
type Progress struct {
	Stage     Stage `json:"stage"`     // 当前阶段
	Current   int   `json:"current"`   // 已处理数量
	Total     int   `json:"total"`     // 总数量
	Available int   `json:"available"` // 当前可用(测通)数量
}

// ProgressFunc 是进度回调；传 nil 表示不需要进度。
type ProgressFunc func(Progress)

// LogFunc 是调试日志回调。仅当 cfg.Debug 为真、且发生上游 -debug 所定义的诊断情形时被调用。
// tone 用于前端着色，对应上游终端的彩色输出：err=红色(错误)、warn=黄色(提示)。传 nil 表示不需要日志。
type LogFunc func(msg, tone string)

// Run 执行一次完整测速：加载 IP 段 → 延迟测速 → 延迟/丢包过滤 → 下载测速 → 排序。
// 通过 ctx 可随时取消；进度经 onProgress 上报；返回最终有序结果。
//
// 流程与 CFST 的 main() 等价：
//
//	NewPing().Run().FilterDelay().FilterLossRate() → TestDownloadSpeed → 排序
func Run(ctx context.Context, cfg Config, onProgress ProgressFunc, onLog LogFunc) ([]Result, error) {
	cfg.Normalize()
	if onProgress == nil {
		onProgress = func(Progress) {}
	}

	r := &runner{
		cfg:        cfg,
		onProgress: onProgress,
		onLog:      onLog,
		colomap:    buildColoMap(cfg.HttpingCFColo),
		maxDelay:   time.Duration(cfg.MaxDelay) * time.Millisecond,
		minDelay:   time.Duration(cfg.MinDelay) * time.Millisecond,
		maxLoss:    float32(cfg.MaxLossRate),
		timeout:    time.Duration(cfg.DownloadTime) * time.Second,
	}

	ips, err := loadIPRanges(cfg)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return []Result{}, nil
	}

	// 延迟测速 + 过滤
	pingSet := r.runPing(ctx, ips)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pingSet = pingSet.filterDelay(r.maxDelay, r.minDelay)
	pingSet = pingSet.filterLossRate(r.maxLoss)

	// 下载测速
	speedSet := r.testDownloadSpeed(ctx, pingSet)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return speedSet.toResults(), nil
}

// runner 持有一次测速运行期间的解析后配置与回调，取代原 CFST 的包级全局变量。
type runner struct {
	cfg        Config
	onProgress ProgressFunc
	onLog      LogFunc
	colomap    map[string]struct{} // -cfcolo 解析后的地区集合; nil 表示不过滤
	maxDelay   time.Duration
	minDelay   time.Duration
	maxLoss    float32
	timeout    time.Duration
}

// debugf 是上游 `if utils.Debug { utils.Xxx.Printf(...) }` 的等价管道：仅在调试模式下，
// 把一条诊断日志（文案逐字照搬上游，去掉行尾换行——每条即一行）经回调送往 Web 日志面板。
// tone 对应上游的终端颜色：err=红、warn=黄。可被多个测速 goroutine 并发调用（回调侧自行保证并发安全）。
func (r *runner) debugf(tone, format string, args ...any) {
	if !r.cfg.Debug || r.onLog == nil {
		return
	}
	r.onLog(fmt.Sprintf(format, args...), tone)
}
