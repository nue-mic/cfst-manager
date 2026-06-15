// Package eventbus 是一个进程内的发布/订阅中枢，用于把测速进度、状态变更与日志
// 推送给 SSE 订阅者。慢订阅者会丢事件而非阻塞发布方。
package eventbus

import "time"

// EventType 事件类型。
type EventType string

const (
	EventRunQueued   EventType = "run.queued"   // 任务入队(排队等待)
	EventRunStarted  EventType = "run.started"  // 测速开始
	EventRunProgress EventType = "run.progress" // 进度更新
	EventRunFinished EventType = "run.finished" // 测速完成(携带结果摘要)
	EventRunFailed   EventType = "run.failed"   // 测速失败
	EventRunStopped  EventType = "run.stopped"  // 被手动中止
	EventQueueChanged EventType = "queue.changed" // 队列变化(入队/取消)
	EventLog         EventType = "log"          // 日志行
)

// Event 是一条广播事件。Data 会在 SSE 层被 JSON 序列化。
type Event struct {
	Seq  uint64    `json:"seq"`
	Type EventType `json:"type"`
	TS   time.Time `json:"ts"`
	Data any       `json:"data,omitempty"`
}
