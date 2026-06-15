package eventbus

import (
	"sync"
	"sync/atomic"
	"time"
)

// Subscription 是 Subscribe 返回的订阅句柄。
type Subscription struct {
	id  uint64
	ch  chan Event
	bus *Bus
}

// C 返回事件投递通道；Unsubscribe 或 Stop 时关闭。
func (s *Subscription) C() <-chan Event { return s.ch }

// Unsubscribe 取消订阅并关闭通道。
func (s *Subscription) Unsubscribe() { s.bus.remove(s.id) }

// Bus 是进程内 pub/sub 中枢。
type Bus struct {
	mu        sync.RWMutex
	subs      map[uint64]*Subscription
	nextSubID uint64
	seq       atomic.Uint64
	dropped   atomic.Uint64

	// lastProgress 缓存最近一次进度/状态事件，便于新订阅者立即获得当前状态。
	lastMu   sync.RWMutex
	lastSnap *Event
}

// New 构造一个 Bus。
func New() *Bus {
	return &Bus{subs: make(map[uint64]*Subscription)}
}

// Publish 把事件广播给所有订阅者。
func (b *Bus) Publish(t EventType, data any) {
	e := Event{
		Seq:  b.seq.Add(1),
		Type: t,
		TS:   time.Now().UTC(),
		Data: data,
	}
	// 记录最近状态快照（进度/状态类）。
	switch t {
	case EventRunStarted, EventRunProgress, EventRunFinished, EventRunFailed, EventRunStopped:
		snap := e
		b.lastMu.Lock()
		b.lastSnap = &snap
		b.lastMu.Unlock()
	}

	b.mu.RLock()
	for _, s := range b.subs {
		select {
		case s.ch <- e:
		default:
			b.dropped.Add(1)
		}
	}
	b.mu.RUnlock()
}

// LastSnapshot 返回最近一次状态/进度事件（无则返回 nil）。
func (b *Bus) LastSnapshot() *Event {
	b.lastMu.RLock()
	defer b.lastMu.RUnlock()
	if b.lastSnap == nil {
		return nil
	}
	cp := *b.lastSnap
	return &cp
}

// Subscribe 注册一个订阅者；bufSize 为每订阅者通道容量。
func (b *Bus) Subscribe(bufSize int) *Subscription {
	if bufSize <= 0 {
		bufSize = 64
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextSubID++
	s := &Subscription{id: b.nextSubID, ch: make(chan Event, bufSize), bus: b}
	b.subs[s.id] = s
	return s
}

func (b *Bus) remove(id uint64) {
	b.mu.Lock()
	if s, ok := b.subs[id]; ok {
		delete(b.subs, id)
		close(s.ch)
	}
	b.mu.Unlock()
}

// Stop 关闭所有订阅通道。
func (b *Bus) Stop() {
	b.mu.Lock()
	for id, s := range b.subs {
		delete(b.subs, id)
		close(s.ch)
	}
	b.mu.Unlock()
}
