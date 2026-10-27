package kernel

import "fmt"

// channels —— 状态通道与 reducer（kernel 的关键设计之一）。
//
// 状态不是拿来覆写的普通 map：一个波内多个节点并发运行，可能同时写同一个 key：
//   - 消息历史应追加而非覆写；
//   - 成本计数器应累加；
//   - 只有当前阶段这类才适合 last-write-wins。
//
// 所以每个 key（通道）声明一个 reducer：(old, new) -> merged。scheduler 在波屏障处
// 用 reducer 折叠一次，使并发结果确定。reducer 必须纯——这也让状态能靠重放事件重建。

// Reducer 把新值纯函数式地折叠进旧值。
type Reducer func(old, neu any) any

func lastReduce(old, neu any) any { return neu }

func appendReduce(old, neu any) any {
	var base []any
	if o, ok := old.([]any); ok {
		base = append(base, o...)
	}
	if n, ok := neu.([]any); ok {
		base = append(base, n...)
	} else {
		base = append(base, neu)
	}
	return base
}

func addReduce(old, neu any) any {
	if isIntish(old) && isIntish(neu) {
		return toInt(old) + toInt(neu)
	}
	o, _ := toFloat(old)
	n, _ := toFloat(neu)
	return o + n
}

func mergeReduce(old, neu any) any {
	out := map[string]any{}
	if o, ok := old.(map[string]any); ok {
		for k, v := range o {
			out[k] = v
		}
	}
	if n, ok := neu.(map[string]any); ok {
		for k, v := range n {
			out[k] = v
		}
	}
	return out
}

// Channel 是状态通道：初值 + reducer + 单位元。
//
// AllowMulti 表示是否允许多个节点在同一波写它。append/add/merge 满足结合律与交换律，
// 因此允许；last 通道并发写的结果取决于调度顺序——即歧义写——默认禁止。
//
// Empty 是 reducer 的单位元，先把一波内对同一通道的多次写聚合成一个"波增量"，
// 使重放不会重复计数。
type Channel struct {
	Init       any
	Reducer    Reducer
	AllowMulti bool
	Empty      any
}

// —— 四种常用通道的工厂；名字即语义。——

// Last：last write wins——单值，如当前阶段或判定。
func Last(init any) Channel {
	return Channel{Init: init, Reducer: lastReduce, AllowMulti: false, Empty: nil}
}

// Append：向列表追加——消息历史、检索到的片段。初值总是非 nil 的 slice
// （对应 Python 的 list(init or [])），因此没人写过的 append 通道读回 [] 而非 nil。
func Append(init any) Channel {
	start := []any{}
	if s, ok := init.([]any); ok {
		start = append(start, s...)
	}
	return Channel{Init: start, Reducer: appendReduce, AllowMulti: true, Empty: []any{}}
}

// Add：数值累加——成本、计数器。
func Add(init any) Channel {
	if init == nil {
		init = 0
	}
	return Channel{Init: init, Reducer: addReduce, AllowMulti: true, Empty: 0}
}

// Merge：map 合并——聚合结构化结果。
func Merge(init any) Channel {
	start := map[string]any{}
	if s, ok := init.(map[string]any); ok {
		for k, v := range s {
			start[k] = v
		}
	}
	return Channel{Init: start, Reducer: mergeReduce, AllowMulti: true, Empty: map[string]any{}}
}

// AmbiguousWriteError：多个节点在同一波写了 last 通道——结果不确定，必须显式处理。
type AmbiguousWriteError struct{ Msg string }

func (e *AmbiguousWriteError) Error() string { return e.Msg }

// write 是一条缓冲的状态增量，带上产出它的节点标记。
type write struct {
	Key    string
	Value  any
	Writer string
}

// WaveWrites 收集一波内每个节点的状态增量，在屏障处折叠一次。节点运行时绝不直接碰
// 共享状态，只把增量交到这里，消除读到别家半成品状态的竞态。
type WaveWrites struct {
	channels map[string]Channel
	buffer   []write
}

// NewWaveWrites 创建绑定某 Plan 通道的空缓冲。
func NewWaveWrites(channels map[string]Channel) *WaveWrites {
	return &WaveWrites{channels: channels}
}

// Buffer 记录一次写。写未声明的通道几乎总是拼写错误或设计遗漏——尽早报错好过默默丢弃。
func (w *WaveWrites) Buffer(key string, value any, writer string) error {
	if _, ok := w.channels[key]; !ok {
		return fmt.Errorf("wrote to undeclared state channel %q (from node %q)", key, writer)
	}
	w.buffer = append(w.buffer, write{Key: key, Value: value, Writer: writer})
	return nil
}

// CheckAmbiguous 在屏障处检查是否有 last 通道被多个节点写过。
func (w *WaveWrites) CheckAmbiguous() error {
	writersByKey := map[string]map[string]bool{}
	for _, wr := range w.buffer {
		if writersByKey[wr.Key] == nil {
			writersByKey[wr.Key] = map[string]bool{}
		}
		writersByKey[wr.Key][wr.Writer] = true
	}
	for key, writers := range writersByKey {
		ch := w.channels[key]
		if !ch.AllowMulti && len(writers) > 1 {
			names := make([]string, 0, len(writers))
			for n := range writers {
				names = append(names, n)
			}
			return &AmbiguousWriteError{Msg: fmt.Sprintf(
				"channel %q is last (last-write-wins) but was written concurrently by "+
					"multiple nodes %v in one wave; the result would depend on scheduling "+
					"order. Use append/add/merge, or let only one node write it.", key, names)}
		}
	}
	return nil
}

// Drain 按到达顺序返回本波的增量并清空缓冲。
func (w *WaveWrites) Drain() []write {
	out := w.buffer
	w.buffer = nil
	return out
}
