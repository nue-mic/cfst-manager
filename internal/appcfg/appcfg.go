// Package appcfg 从环境变量加载守护进程自身的运行配置。
package appcfg

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config 是 cfstmgrd 的运行配置（全部可由环境变量覆盖）。
type Config struct {
	HTTPAddr     string   // CFST_HTTP_ADDR     监听地址 (默认 :18123)
	HTTPAddrWarn string   // 地址归一化告警(非致命)
	APIToken     string   // CFST_API_TOKEN     管理端 Bearer 令牌(空则自动生成并落盘)
	CORSOrigins  []string // CFST_CORS_ORIGINS  允许跨域来源 (默认 *)
	DataDir      string   // CFST_DATA_DIR      数据根目录 (默认 data)
	LogLevel     string   // CFST_LOG_LEVEL     trace|debug|info|warn|error (默认 info)
	DocsEnabled  bool     // CFST_DOCS_ENABLED  是否暴露 /api/docs (默认 true)
	PublicOpen   bool     // CFST_PUBLIC_OPEN   公开API开放模式:接受任意 key (默认 false)
	ShutdownWait time.Duration

	// 派生路径
	RunsDir     string // 历史测速记录
	IPSourceDir string // 自定义 IP 源文件
	MetaFile    string // 设置 + Profile + License 元数据
}

const defaultListenAddr = ":18123"

// Load 读取环境变量并填充配置。
func Load() *Config {
	httpAddr, warn := NormalizeListenAddr(getEnv("CFST_HTTP_ADDR", defaultListenAddr))
	dataDir := getEnv("CFST_DATA_DIR", "data")
	cfg := &Config{
		HTTPAddr:     httpAddr,
		HTTPAddrWarn: warn,
		APIToken:     strings.TrimSpace(os.Getenv("CFST_API_TOKEN")),
		CORSOrigins:  splitCSV(getEnv("CFST_CORS_ORIGINS", "*")),
		DataDir:      dataDir,
		LogLevel:     strings.ToLower(getEnv("CFST_LOG_LEVEL", "info")),
		DocsEnabled:  parseBool(getEnv("CFST_DOCS_ENABLED", "true"), true),
		PublicOpen:   parseBool(getEnv("CFST_PUBLIC_OPEN", "false"), false),
		ShutdownWait: 10 * time.Second,
	}
	cfg.RunsDir = filepath.Join(dataDir, "runs")
	cfg.IPSourceDir = filepath.Join(dataDir, "ipsources")
	cfg.MetaFile = filepath.Join(dataDir, "meta.json")
	return cfg
}

// EnsureDirs 创建数据子目录。
func (c *Config) EnsureDirs() error {
	for _, d := range []string{c.DataDir, c.RunsDir, c.IPSourceDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// EnsureAPIToken 确保有管理端令牌：CFST_API_TOKEN 为空时，从 <data>/admin_token
// 读取已有令牌；若也不存在则随机生成一个并落盘。返回最终令牌与是否为新生成。
func (c *Config) EnsureAPIToken() (token string, generated bool, err error) {
	if c.APIToken != "" {
		return c.APIToken, false, nil
	}
	tokenPath := filepath.Join(c.DataDir, "admin_token")
	if b, e := os.ReadFile(tokenPath); e == nil {
		if t := strings.TrimSpace(string(b)); t != "" {
			c.APIToken = t
			return t, false, nil
		}
	}
	buf := make([]byte, 24)
	if _, err = rand.Read(buf); err != nil {
		return "", false, err
	}
	t := hex.EncodeToString(buf)
	if err = os.WriteFile(tokenPath, []byte(t+"\n"), 0o600); err != nil {
		return "", false, err
	}
	c.APIToken = t
	return t, true, nil
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// NormalizeListenAddr 让 CFST_HTTP_ADDR 更宽容：裸端口("18123")自动补":"。
// 无法识别的值原样返回并附告警，交给 net.Listen 报真实错误。
func NormalizeListenAddr(raw string) (addr string, warn string) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return defaultListenAddr, ""
	}
	if isAllASCIIDigits(s) {
		if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= 65535 {
			return ":" + s, ""
		}
		return s, "CFST_HTTP_ADDR 端口越界(需 1-65535): " + raw
	}
	if _, port, err := net.SplitHostPort(s); err == nil {
		if p, e := strconv.Atoi(port); e == nil && p >= 1 && p <= 65535 {
			return s, ""
		}
		return s, "CFST_HTTP_ADDR 端口部分非法: " + raw
	}
	return s, "CFST_HTTP_ADDR 无法识别: " + raw + " (IPv6 单地址须用 [addr]:port)"
}

func isAllASCIIDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// ParseLevel 把级别名映射为 slog.Level。
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "trace", "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func parseBool(s string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on", "y":
		return true
	case "0", "false", "no", "off", "n":
		return false
	default:
		return def
	}
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
