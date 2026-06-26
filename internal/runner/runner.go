// Package runner 是测速任务管理器：维护一个全局串行队列，由单个 worker 一次只跑一个测速，
// 其余任务（无论手动还是定时触发）排队等待，上一个结束后自动开始下一个。
// 负责取消任务、把进度/状态推送到事件总线、并将结果落盘到 store。
package runner

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nue-mic/cfst-manager/internal/engine"
	"github.com/nue-mic/cfst-manager/internal/eventbus"
	"github.com/nue-mic/cfst-manager/internal/store"
)

// SubmitOpts 是提交一个测速任务的参数。
type SubmitOpts struct {
	Config     engine.Config
	Profile    string
	Note       string
	Trigger    string // 人类可读触发来源，如 "手动" / "定时:每日优选"
	ScheduleID string // 非空表示来自定时任务（用于回写该任务的最近状态）
	Publish    bool   // 完成后是否显式发布到对应 profile（定时任务用；手动依赖全局自动发布）
}

// QueuedJob 是队列中的一个任务。
type QueuedJob struct {
	RunID      string
	Profile    string
	Note       string
	Trigger    string
	ScheduleID string
	Publish    bool
	Config     engine.Config
	EnqueuedAt time.Time
}

// JobInfo 是对外暴露的任务信息（用于状态展示）。
type JobInfo struct {
	RunID      string     `json:"run_id"`
	Profile    string     `json:"profile"`
	Note       string     `json:"note,omitempty"`
	Trigger    string     `json:"trigger,omitempty"`
	EnqueuedAt time.Time  `json:"enqueued_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
}

// Status 是当前运行状态：正在跑的任务 + 排队中的任务。
type Status struct {
	Running  bool             `json:"running"`
	Active   *JobInfo         `json:"active,omitempty"`
	Progress *engine.Progress `json:"progress,omitempty"`
	Queue    []JobInfo        `json:"queue"`
}

// Runner 管理测速任务队列。
type Runner struct {
	store *store.Store
	bus   *eventbus.Bus
	log   *slog.Logger

	mu          sync.Mutex
	queue       []*QueuedJob
	active      *QueuedJob
	activeStart time.Time
	cancel      context.CancelFunc
	progress    engine.Progress

	wake chan struct{}
}

// New 构造 Runner 并启动后台 worker。
func New(st *store.Store, bus *eventbus.Bus, log *slog.Logger) *Runner {
	r := &Runner{store: st, bus: bus, log: log, wake: make(chan struct{}, 1)}
	go r.worker()
	return r
}

func (r *Runner) signal() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Submit 提交一个测速任务进队列。返回 run id 与前面排队的任务数（0 表示将立即开始）。
func (r *Runner) Submit(o SubmitOpts) (runID string, ahead int) {
	o.Config.Normalize()
	runID = store.NewRunID(time.Now().UTC())
	job := &QueuedJob{
		RunID: runID, Profile: o.Profile, Note: o.Note, Trigger: o.Trigger,
		ScheduleID: o.ScheduleID, Publish: o.Publish, Config: o.Config,
		EnqueuedAt: time.Now().UTC(),
	}
	r.mu.Lock()
	r.queue = append(r.queue, job)
	ahead = len(r.queue) - 1
	if r.active != nil {
		ahead++
	}
	r.mu.Unlock()

	r.signal()
	if ahead > 0 {
		r.bus.Publish(eventbus.EventRunQueued, map[string]any{
			"run_id": runID, "profile": o.Profile, "trigger": o.Trigger, "ahead": ahead,
		})
		r.logf("已加入队列 run=%s 触发=%s 前面还有 %d 个", runID, o.Trigger, ahead)
	}
	r.publishQueueChanged()
	return runID, ahead
}

// HasScheduleJob 报告某定时任务是否已有在跑或在排队的任务（用于防重叠触发）。
func (r *Runner) HasScheduleJob(scheduleID string) bool {
	if scheduleID == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active != nil && r.active.ScheduleID == scheduleID {
		return true
	}
	for _, j := range r.queue {
		if j.ScheduleID == scheduleID {
			return true
		}
	}
	return false
}

func (r *Runner) worker() {
	for range r.wake {
		for {
			r.mu.Lock()
			if len(r.queue) == 0 {
				r.mu.Unlock()
				break
			}
			job := r.queue[0]
			r.queue = r.queue[1:]
			ctx, cancel := context.WithCancel(context.Background())
			r.active = job
			r.activeStart = time.Now().UTC()
			r.cancel = cancel
			r.progress = engine.Progress{}
			r.mu.Unlock()

			r.publishQueueChanged()
			r.execute(ctx, job)

			r.mu.Lock()
			r.active = nil
			r.cancel = nil
			r.mu.Unlock()
			r.publishQueueChanged()
		}
	}
}

func (r *Runner) execute(ctx context.Context, job *QueuedJob) {
	r.bus.Publish(eventbus.EventRunStarted, map[string]any{
		"run_id": job.RunID, "profile": job.Profile, "note": job.Note,
		"trigger": job.Trigger, "config": job.Config,
	})
	r.logf("测速开始 run=%s 触发=%s", job.RunID, job.Trigger)

	onProgress := func(p engine.Progress) {
		r.mu.Lock()
		r.progress = p
		r.mu.Unlock()
		r.bus.Publish(eventbus.EventRunProgress, p)
	}

	// 调试日志回调：仅当本次任务开启 -debug 时由引擎调用，逐条转发到事件总线 → SSE → 前端日志面板。
	// 可能被多个测速 goroutine 并发调用；bus.Publish 自身并发安全，慢订阅者丢事件而非阻塞。
	onLog := func(msg, tone string) {
		if r.log != nil {
			r.log.Debug("engine: " + msg) // slog 默认 Info 级，调试行默认被过滤，不污染服务端日志
		}
		r.bus.Publish(eventbus.EventLog, map[string]any{"msg": msg, "level": "debug", "tone": tone})
	}

	// 守护进程级保护：极端畸形 IP 输入理论上可能触发 panic，降级为一次"失败"记录。
	var results []engine.Result
	var err error
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				err = fmt.Errorf("测速引擎异常: %v", rec)
				r.logf("测速引擎 panic 已捕获 run=%s: %v", job.RunID, rec)
			}
		}()
		results, err = engine.Run(ctx, job.Config, onProgress, onLog)
	}()

	finishedAt := time.Now().UTC()
	rec := store.Run{
		ID: job.RunID, CreatedAt: r.activeStart, FinishedAt: finishedAt,
		Profile: job.Profile, Note: job.Note, Trigger: job.Trigger,
		Config: job.Config, Results: results,
	}
	switch {
	case ctx.Err() != nil:
		rec.Status = "stopped"
		r.logf("测速已中止 run=%s", job.RunID)
	case err != nil:
		rec.Status = "failed"
		rec.Error = err.Error()
		r.logf("测速失败 run=%s err=%v", job.RunID, err)
	default:
		rec.Status = "finished"
		r.logf("测速完成 run=%s 命中 %d 个 IP", job.RunID, len(results))
	}
	rec.IPVersion = store.DetectIPVersion(rec.Results)

	if err := r.store.SaveRun(rec); err != nil {
		r.logf("保存测速记录失败 run=%s err=%v", job.RunID, err)
	}

	// 定时任务的显式发布（独立于全局自动发布开关）。
	if job.Publish && rec.Status == "finished" && len(rec.Results) > 0 && job.Profile != "" {
		if err := r.store.Publish(job.Profile, rec.IPVersion, job.RunID); err != nil {
			r.logf("发布失败 run=%s err=%v", job.RunID, err)
		}
	}

	// 回写定时任务的最近运行状态。
	if job.ScheduleID != "" {
		r.store.UpdateScheduleLastRun(job.ScheduleID, job.RunID, rec.Status, finishedAt)
	}

	top := rec.Results
	if len(top) > 20 {
		top = top[:20]
	}
	evtData := map[string]any{
		"run_id": job.RunID, "status": rec.Status, "error": rec.Error,
		"profile": job.Profile, "ip_version": rec.IPVersion, "trigger": job.Trigger,
		"count": len(rec.Results), "top": top,
	}
	switch rec.Status {
	case "failed":
		r.bus.Publish(eventbus.EventRunFailed, evtData)
	case "stopped":
		r.bus.Publish(eventbus.EventRunStopped, evtData)
	default:
		r.bus.Publish(eventbus.EventRunFinished, evtData)
	}
}

// Stop 中止当前正在运行的任务（队列中的后续任务会继续）。无运行任务返回 false。
func (r *Runner) Stop() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil || r.cancel == nil {
		return false
	}
	r.cancel()
	return true
}

// CancelQueued 从队列移除一个尚未开始的任务。
func (r *Runner) CancelQueued(runID string) bool {
	r.mu.Lock()
	removed := false
	for i, j := range r.queue {
		if j.RunID == runID {
			r.queue = append(r.queue[:i], r.queue[i+1:]...)
			removed = true
			break
		}
	}
	r.mu.Unlock()
	if removed {
		r.publishQueueChanged()
	}
	return removed
}

// ClearQueue 清空所有排队中的任务（不影响正在运行的）。返回清除数量。
func (r *Runner) ClearQueue() int {
	r.mu.Lock()
	n := len(r.queue)
	r.queue = nil
	r.mu.Unlock()
	if n > 0 {
		r.publishQueueChanged()
	}
	return n
}

// Status 返回当前运行状态。
func (r *Runner) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := Status{Running: r.active != nil, Queue: []JobInfo{}}
	if r.active != nil {
		started := r.activeStart
		st.Active = &JobInfo{
			RunID: r.active.RunID, Profile: r.active.Profile, Note: r.active.Note,
			Trigger: r.active.Trigger, EnqueuedAt: r.active.EnqueuedAt, StartedAt: &started,
		}
		p := r.progress
		st.Progress = &p
	}
	for _, j := range r.queue {
		st.Queue = append(st.Queue, JobInfo{
			RunID: j.RunID, Profile: j.Profile, Note: j.Note,
			Trigger: j.Trigger, EnqueuedAt: j.EnqueuedAt,
		})
	}
	return st
}

func (r *Runner) publishQueueChanged() {
	r.bus.Publish(eventbus.EventQueueChanged, r.Status())
}

func (r *Runner) logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if r.log != nil {
		r.log.Info("runner: " + msg)
	}
	r.bus.Publish(eventbus.EventLog, map[string]any{"msg": msg})
}
