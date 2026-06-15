// Package scheduler 用 cron 驱动定时测速任务：到点把任务提交进 runner 的串行队列。
// 队列保证一次只跑一个；若上次同名任务尚未结束，本次触发会被跳过以防堆积。
package scheduler

import (
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/mia-clark/cfst-manager/internal/runner"
	"github.com/mia-clark/cfst-manager/internal/store"
)

// Scheduler 管理全部定时任务的 cron 注册。
type Scheduler struct {
	store   *store.Store
	sources *store.SourceManager
	runner  *runner.Runner
	log     *slog.Logger

	mu      sync.Mutex
	cron    *cron.Cron
	entries map[string]cron.EntryID // schedule id -> cron entry
}

// New 构造 Scheduler。
func New(st *store.Store, sources *store.SourceManager, run *runner.Runner, log *slog.Logger) *Scheduler {
	return &Scheduler{
		store: st, sources: sources, runner: run, log: log,
		cron:    cron.New(),
		entries: map[string]cron.EntryID{},
	}
}

// ValidateSpec 校验 cron 表达式是否合法（支持标准 5 段与 @every/@daily 等描述符）。
func ValidateSpec(spec string) error {
	_, err := cron.ParseStandard(strings.TrimSpace(spec))
	return err
}

// Start 加载全部启用的定时任务并启动调度。
func (s *Scheduler) Start() {
	s.Reload()
	s.cron.Start()
}

// Stop 停止调度。
func (s *Scheduler) Stop() {
	ctx := s.cron.Stop()
	<-ctx.Done()
}

// Reload 依据 store 中的定时任务重建 cron 注册（增删改/启停后调用）。
func (s *Scheduler) Reload() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.entries {
		s.cron.Remove(id)
	}
	s.entries = map[string]cron.EntryID{}
	for _, sc := range s.store.ListSchedules() {
		if !sc.Enabled {
			continue
		}
		scID := sc.ID
		eid, err := s.cron.AddFunc(strings.TrimSpace(sc.Spec), func() { s.fire(scID) })
		if err != nil {
			s.log.Warn("定时任务表达式无效，已跳过", slog.String("schedule", sc.Name), slog.String("spec", sc.Spec), slog.Any("err", err))
			continue
		}
		s.entries[scID] = eid
	}
}

// NextRuns 返回每个已启用定时任务的下次运行时间（schedule id -> 时间）。
func (s *Scheduler) NextRuns() map[string]time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]time.Time{}
	for scID, eid := range s.entries {
		e := s.cron.Entry(eid)
		if !e.Next.IsZero() {
			out[scID] = e.Next
		}
	}
	return out
}

// Trigger 立即手动触发某个定时任务（“立即运行”按钮用）。返回是否成功提交。
func (s *Scheduler) Trigger(scheduleID string) (string, bool) {
	return s.fireResult(scheduleID)
}

func (s *Scheduler) fire(scheduleID string) {
	s.fireResult(scheduleID)
}

func (s *Scheduler) fireResult(scheduleID string) (string, bool) {
	sc, ok := s.store.GetSchedule(scheduleID)
	if !ok {
		return "", false
	}
	// 防重叠：同一定时任务上次还在跑/排队，则跳过本次触发。
	if s.runner.HasScheduleJob(scheduleID) {
		s.log.Info("定时任务上次尚未结束，跳过本次触发", slog.String("schedule", sc.Name))
		return "", false
	}

	cfg := sc.Config
	if strings.TrimSpace(sc.IPText) != "" {
		cfg.IPText = sc.IPText
	} else {
		name := strings.TrimSpace(sc.IPSource)
		if name == "" {
			name = "ip.txt"
		}
		path, err := s.sources.Path(name)
		if err != nil {
			s.log.Warn("定时任务 IP 源不存在，跳过", slog.String("schedule", sc.Name), slog.String("ip_source", name))
			return "", false
		}
		cfg.IPFile = path
	}

	runID, ahead := s.runner.Submit(runner.SubmitOpts{
		Config: cfg, Profile: sc.Profile, Note: sc.Note,
		Trigger: "定时:" + sc.Name, ScheduleID: sc.ID, Publish: sc.Publish,
	})
	s.store.UpdateScheduleLastRun(sc.ID, runID, "queued", time.Now().UTC())
	s.log.Info("定时任务已触发", slog.String("schedule", sc.Name), slog.String("run", runID), slog.Int("ahead", ahead))
	return runID, true
}
