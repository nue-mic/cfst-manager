package store

import (
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/mia-clark/cfst-manager/internal/engine"
)

// ErrNotFound 表示记录不存在。
var ErrNotFound = errors.New("记录不存在")

// DetectIPVersion 依据结果中的 IP 判断版本：v4 / v6 / mixed / ""(空)。
func DetectIPVersion(results []engine.Result) string {
	var v4, v6 int
	for _, r := range results {
		if strings.Contains(r.IP, ":") {
			v6++
		} else if r.IP != "" {
			v4++
		}
	}
	switch {
	case v4 > 0 && v6 > 0:
		return "mixed"
	case v6 > 0:
		return "v6"
	case v4 > 0:
		return "v4"
	default:
		return ""
	}
}

// SaveRun 持久化一次测速记录，更新索引；若开启自动发布且成功则发布到对应 Profile。
func (s *Store) SaveRun(r Run) error {
	if r.IPVersion == "" {
		r.IPVersion = DetectIPVersion(r.Results)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := writeJSONAtomic(s.runPath(r.ID), r); err != nil {
		return err
	}
	// 更新索引（去重后置顶）
	sum := summarize(r)
	out := s.index[:0]
	for _, it := range s.index {
		if it.ID != r.ID {
			out = append(out, it)
		}
	}
	s.index = append([]RunSummary{sum}, out...)
	s.sortIndexLocked()
	if err := s.persistIndexLocked(); err != nil {
		return err
	}

	// 自动发布
	if s.meta.Settings.AutoPublish && r.Status == "finished" && len(r.Results) > 0 && r.Profile != "" {
		s.publishLocked(r.Profile, r.IPVersion, r.ID)
		_ = s.persistMetaLocked()
	}
	return nil
}

// ListRuns 返回历史摘要（按时间倒序）。profile 非空时按 profile 过滤。
func (s *Store) ListRuns(profile string) []RunSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]RunSummary, 0, len(s.index))
	for _, it := range s.index {
		if profile != "" && it.Profile != profile {
			continue
		}
		out = append(out, it)
	}
	return out
}

// GetRun 读取完整记录。
func (s *Store) GetRun(id string) (Run, error) {
	s.mu.RLock()
	path := s.runPath(id)
	s.mu.RUnlock()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Run{}, ErrNotFound
		}
		return Run{}, err
	}
	var r Run
	if err := json.Unmarshal(b, &r); err != nil {
		return Run{}, err
	}
	return r, nil
}

// DeleteRun 删除记录及其在任何 Profile 上的发布引用。
func (s *Store) DeleteRun(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.Remove(s.runPath(id)); err != nil && !os.IsNotExist(err) {
		return err
	}
	out := s.index[:0]
	found := false
	for _, it := range s.index {
		if it.ID == id {
			found = true
			continue
		}
		out = append(out, it)
	}
	s.index = out
	if err := s.persistIndexLocked(); err != nil {
		return err
	}
	// 清理发布引用
	changed := false
	for _, p := range s.meta.Profiles {
		if p.PublishedV4RunID == id {
			p.PublishedV4RunID = ""
			changed = true
		}
		if p.PublishedV6RunID == id {
			p.PublishedV6RunID = ""
			changed = true
		}
		changed = clearLineRefs(p.LineV4, id) || changed
		changed = clearLineRefs(p.LineV6, id) || changed
	}
	if changed {
		_ = s.persistMetaLocked()
	}
	if !found {
		return ErrNotFound
	}
	return nil
}

func clearLineRefs(m map[string]string, id string) bool {
	changed := false
	for k, v := range m {
		if v == id {
			delete(m, k)
			changed = true
		}
	}
	return changed
}
