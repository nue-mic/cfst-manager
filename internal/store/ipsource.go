package store

import (
	"embed"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed builtin/ip.txt builtin/ipv6.txt
var builtinFS embed.FS

// 内置 IP 源（来自 XIU2/CloudflareSpeedTest 的 Cloudflare 官方 IP 段）。
var builtinSources = []string{"ip.txt", "ipv6.txt"}

// IPSource 描述一个 IP 源文件。
type IPSource struct {
	Name    string `json:"name"`     // 文件名
	Size    int64  `json:"size"`     // 字节数
	Lines   int    `json:"lines"`    // 非空行数(IP段数)
	Builtin bool   `json:"builtin"`  // 是否内置默认源
}

// SourceManager 管理 IPSourceDir 下的 IP 源文件。
type SourceManager struct {
	dir string
}

// NewSourceManager 构造并把内置源播种到磁盘(若不存在)。
func NewSourceManager(dir string) (*SourceManager, error) {
	m := &SourceManager{dir: dir}
	for _, name := range builtinSources {
		dst := filepath.Join(dir, name)
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		b, err := builtinFS.ReadFile("builtin/" + name)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// validName 防目录穿越，仅允许简单文件名。
func validName(name string) bool {
	if name == "" || strings.ContainsAny(name, "/\\") || name == "." || name == ".." {
		return false
	}
	return name == filepath.Base(name)
}

func isBuiltin(name string) bool {
	for _, b := range builtinSources {
		if b == name {
			return true
		}
	}
	return false
}

// List 列出全部 IP 源。
func (m *SourceManager) List() ([]IPSource, error) {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return nil, err
	}
	out := []IPSource{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(m.dir, e.Name()))
		out = append(out, IPSource{
			Name: e.Name(), Size: info.Size(),
			Lines: countNonEmptyLines(b), Builtin: isBuiltin(e.Name()),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Path 返回某 IP 源的磁盘路径(供引擎读取)。
func (m *SourceManager) Path(name string) (string, error) {
	if !validName(name) {
		return "", errors.New("非法的 IP 源名称")
	}
	p := filepath.Join(m.dir, name)
	if _, err := os.Stat(p); err != nil {
		return "", ErrNotFound
	}
	return p, nil
}

// Read 读取 IP 源内容。
func (m *SourceManager) Read(name string) (string, error) {
	if !validName(name) {
		return "", errors.New("非法的 IP 源名称")
	}
	b, err := os.ReadFile(filepath.Join(m.dir, name))
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrNotFound
		}
		return "", err
	}
	return string(b), nil
}

// Write 新建/覆盖 IP 源内容。
func (m *SourceManager) Write(name, content string) error {
	if !validName(name) {
		return errors.New("非法的 IP 源名称")
	}
	return os.WriteFile(filepath.Join(m.dir, name), []byte(content), 0o644)
}

// Delete 删除 IP 源（内置源不可删）。
func (m *SourceManager) Delete(name string) error {
	if !validName(name) {
		return errors.New("非法的 IP 源名称")
	}
	if isBuiltin(name) {
		return errors.New("内置 IP 源不可删除")
	}
	if err := os.Remove(filepath.Join(m.dir, name)); err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func countNonEmptyLines(b []byte) int {
	n := 0
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}
