package kernel

import (
	"sync"
	"time"
)

// eventlog —— Event 是事实源；state 不单独存，而是把 Event 流从头逐条累加算出来的结果。
//
// 三部分：
//   - Event：不可变的"已发生的事实"，只追加、永不修改；
//   - ApplyEvent：把单个 Event 累加进 shared state 的纯函数；
//   - EventLog 存储接口加进程内默认实现。
//
// 刻意不提供快照存储：仅凭事件流即可重建每个 Run（见 replay）。
// 由 Event 逐条累加出 state，同时得到审计、时间旅行与崩溃恢复。

// Event 种类（教学版只保留最有代表性的）。
const (
	EvRunStarted      = "run_started"
	EvNodeStarted     = "node_started"
	EvNodeCompleted   = "node_completed"
	EvNodeFailed      = "node_failed"
	EvNodeSkipped     = "node_skipped"
	EvNodeRetry       = "node_retry"
	EvStateDelta      = "state_delta"
	EvDelegated       = "delegated" // Node 生成了子 Run（崩溃重挂锚点）
	EvControl         = "control"   // 实际发生过的控制命令
	EvArtifactWritten = "artifact_written"
	EvInterrupted     = "interrupted"
	EvResumed         = "resumed"
	EvRunCompleted    = "run_completed"
	EvRunFailed       = "run_failed"
)

// epoch 锚定 Event 时间戳所用的单调时钟；Trace 用相减得出时长。
var epoch = time.Now()

func monotonic() float64 { return time.Since(epoch).Seconds() }

// Event 是一条不可变事实，只追加、永不修改。
type Event struct {
	Seq      int            // 单个 Run 内单调递增
	RunID    string         //
	Kind     string         //
	Data     map[string]any //
	ParentID string         // 子 Run 的 Event 挂到父 Run（"" = 无）
	Ts       float64        // 记录时刻
}

// NewEvent 给 Event 打上当前单调时间戳。
func NewEvent(seq int, runID, kind string, data map[string]any, parentID string) Event {
	if data == nil {
		data = map[string]any{}
	}
	return Event{Seq: seq, RunID: runID, Kind: kind, Data: data, ParentID: parentID, Ts: monotonic()}
}

// ApplyEvent 把单个 Event 折叠进 shared（原地修改）。纯函数：同一 Event 流
// 总产出相同的 state。
func ApplyEvent(shared map[string]any, event Event, channels map[string]Channel) {
	if event.Kind != EvStateDelta {
		return
	}
	delta, _ := event.Data["delta"].(map[string]any)
	for key, value := range delta {
		if ch, ok := channels[key]; ok {
			shared[key] = ch.Reducer(shared[key], value)
		}
	}
}

// FoldEvents 从初始 state 重放整个 Event 流。
func FoldEvents(events []Event, channels map[string]Channel, initial map[string]any) map[string]any {
	shared := copyMap(initial)
	for _, ev := range events {
		ApplyEvent(shared, ev, channels)
	}
	return shared
}

// EventLog 是生产后端（Redis/Postgres）须满足的存储契约；内核自带内存默认实现。
type EventLog interface {
	Append(event Event) error
	Events(runID string) ([]Event, error)
	AllEvents() ([]Event, error) // 所有 Run 的事实——跨 Run 的 Trace 投影
}

// InMemoryEventLog 是进程内 Event 日志，默认实现。
type InMemoryEventLog struct {
	mu      sync.Mutex
	streams map[string][]Event
	order   []string // 按首次追加顺序记录的 Run ID，保证 AllEvents 确定性
}

// NewInMemoryEventLog 构造空的进程内日志。
func NewInMemoryEventLog() *InMemoryEventLog {
	return &InMemoryEventLog{streams: map[string][]Event{}}
}

func (l *InMemoryEventLog) Append(event Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, seen := l.streams[event.RunID]; !seen {
		l.order = append(l.order, event.RunID)
	}
	l.streams[event.RunID] = append(l.streams[event.RunID], event)
	return nil
}

func (l *InMemoryEventLog) Events(runID string) ([]Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Event, len(l.streams[runID]))
	copy(out, l.streams[runID])
	return out, nil
}

func (l *InMemoryEventLog) AllEvents() ([]Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Event
	// 按首次追加顺序遍历（与 Python dict 的插入序一致），使跨 Run 的 Trace 投影
	// 确定，而非随 map 乱序。
	for _, runID := range l.order {
		out = append(out, l.streams[runID]...)
	}
	return out, nil
}
