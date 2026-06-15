package store

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/mia-clark/cfst-manager/internal/engine"
)

// Schedule 是一个定时测速任务。Spec 支持标准 5 段 cron 或 @every/@daily 等描述符。
type Schedule struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Enabled   bool          `json:"enabled"`
	Spec      string        `json:"spec"`       // cron 表达式，如 "0 */6 * * *" 或 "@every 30m"
	Profile   string        `json:"profile"`    // 目标 CDN profile
	IPSource  string        `json:"ip_source"`  // IP 源文件名（与 ip_text 二选一）
	IPText    string        `json:"ip_text"`    // 直接指定 IP 段（优先）
	Publish   bool          `json:"publish"`    // 完成后是否发布到对应 profile
	Note      string        `json:"note"`       // 备注
	Config    engine.Config `json:"config"`     // 测速参数
	CreatedAt time.Time     `json:"created_at"`
	LastRunAt time.Time     `json:"last_run_at"`
	LastRunID string        `json:"last_run_id"`
	LastStatus string       `json:"last_status"` // queued|finished|failed|stopped
}

// ListSchedules 返回全部定时任务副本。
func (s *Store) ListSchedules() []Schedule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Schedule, len(s.meta.Schedules))
	copy(out, s.meta.Schedules)
	return out
}

// GetSchedule 返回指定定时任务。
func (s *Store) GetSchedule(id string) (Schedule, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, sc := range s.meta.Schedules {
		if sc.ID == id {
			return sc, true
		}
	}
	return Schedule{}, false
}

// AddSchedule 新增定时任务（自动生成 ID）。
func (s *Store) AddSchedule(sc Schedule) (Schedule, error) {
	sc.ID = "sch-" + randHex(4)
	sc.CreatedAt = time.Now().UTC()
	sc.Config.Normalize()
	if sc.Name == "" {
		sc.Name = sc.ID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.meta.Schedules = append(s.meta.Schedules, sc)
	if err := s.persistMetaLocked(); err != nil {
		return Schedule{}, err
	}
	return sc, nil
}

// UpdateSchedule 覆盖更新指定定时任务（保留 ID/CreatedAt/Last* 等运行态字段）。
func (s *Store) UpdateSchedule(id string, in Schedule) (Schedule, error) {
	in.Config.Normalize()
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.meta.Schedules {
		if s.meta.Schedules[i].ID == id {
			cur := s.meta.Schedules[i]
			in.ID = cur.ID
			in.CreatedAt = cur.CreatedAt
			in.LastRunAt = cur.LastRunAt
			in.LastRunID = cur.LastRunID
			in.LastStatus = cur.LastStatus
			if in.Name == "" {
				in.Name = cur.Name
			}
			s.meta.Schedules[i] = in
			if err := s.persistMetaLocked(); err != nil {
				return Schedule{}, err
			}
			return in, nil
		}
	}
	return Schedule{}, ErrNotFound
}

// DeleteSchedule 删除定时任务。
func (s *Store) DeleteSchedule(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.meta.Schedules[:0]
	found := false
	for _, sc := range s.meta.Schedules {
		if sc.ID == id {
			found = true
			continue
		}
		out = append(out, sc)
	}
	s.meta.Schedules = out
	if !found {
		return ErrNotFound
	}
	return s.persistMetaLocked()
}

// ToggleSchedule 启用/停用定时任务。
func (s *Store) ToggleSchedule(id string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.meta.Schedules {
		if s.meta.Schedules[i].ID == id {
			s.meta.Schedules[i].Enabled = enabled
			return s.persistMetaLocked()
		}
	}
	return ErrNotFound
}

// UpdateScheduleLastRun 记录某定时任务最近一次运行的状态。
func (s *Store) UpdateScheduleLastRun(id, runID, status string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.meta.Schedules {
		if s.meta.Schedules[i].ID == id {
			s.meta.Schedules[i].LastRunID = runID
			s.meta.Schedules[i].LastStatus = status
			s.meta.Schedules[i].LastRunAt = at
			_ = s.persistMetaLocked()
			return
		}
	}
}

func randHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return strings.ToLower(hex.EncodeToString(buf))
}
