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

// Reducer 把新值纯函数式地折叠进旧值 —— 并发写入的"合并规则"都收敛到这一个签名。
// 必须纯：同样的 (old, neu) 永远给出同样的结果，重放才能一致。
type Reducer func(old, neu any) any

// lastReduce 忽略 old，永远返回 neu —— 简单"覆盖"语义。
// 例子：("old", "new") → "new"；("阶段A", "阶段B") → "阶段B"。
//
// 由于结果取决于"谁是最后一个"，同波多节点并发写不满足交换律，
// 需要在 WaveWrites.CheckAmbiguous 里显式拦截。
func lastReduce(old, neu any) any { return neu }

// appendReduce 把 neu 拼到 old 列表尾巴。
// 例子：([a], [b,c]) → [a,b,c]；([a], "b") → [a,b]（neu 为单元素也吃下）；
//
//	(nil, [b]) → [b]（首次写时 old 可能为 nil）。
//
// 满足结合律 + 交换律 → AllowMulti=true：并发写顺序不影响最终列表内容。
func appendReduce(old, neu any) any {
	var base []any
	if o, ok := old.([]any); ok {
		base = append(base, o...) // old 是列表：先拷进来，避免就地修改调用方持有的切片
	}
	if n, ok := neu.([]any); ok {
		base = append(base, n...) // neu 是列表：整段追加
	} else {
		base = append(base, neu) // neu 是单值：当成一个元素追加
	}
	return base
}

// addReduce 数值累加 —— 成本/token 计数的默认折叠。
// 例子：(1, 2) → 3；(0.5, 1.5) → 2.0。
// 优先走 int 分支保留整数类型；任一操作数是 float 或非整数则统一浮点。
//
// 满足结合律 + 交换律 → AllowMulti=true。
func addReduce(old, neu any) any {
	if isIntish(old) && isIntish(neu) {
		return toInt(old) + toInt(neu) // 两边都像整数：结果保持 int
	}
	o, _ := toFloat(old)
	n, _ := toFloat(neu)
	return o + n // 否则统一走 float
}

// mergeReduce 合并两个 map，neu 里的同键覆盖 old。
// 例子：({a:1}, {b:2}) → {a:1,b:2}；({a:1}, {a:9}) → {a:9}；(nil, {a:1}) → {a:1}。
//
// 整体满足结合律 + 交换律 → AllowMulti=true；但"同键并发写"仍然后写覆盖，
// 使用时约定"不同节点写不同键"才不会丢数据。
func mergeReduce(old, neu any) any {
	out := map[string]any{}
	if o, ok := old.(map[string]any); ok {
		for k, v := range o {
			out[k] = v // 第一步：把 old 全量拷进 out（不动 old 本身）
		}
	}
	if n, ok := neu.(map[string]any); ok {
		for k, v := range n {
			out[k] = v // 第二步：neu 覆盖同名键
		}
	}
	return out
}

// Channel 是状态通道：初值 + reducer + 单位元。
type Channel struct {
	// Init 是通道在 Run 起动时的初值（写入 InitialShared → Run.Shared）。
	Init any
	// Reducer 是本通道 (old, neu) → merged 的折叠规则，必须纯。
	Reducer Reducer
	// AllowMulti 表示是否允许多个节点在同一波写它。
	// append/add/merge 满足结合律与交换律 → 允许（true）；
	// last 通道的结果取决于调度顺序 → 禁止（false），由 CheckAmbiguous 拦下。
	AllowMulti bool
	// Empty 是 reducer 的单位元：把一波内对同一通道的多次写先聚合成一个
	// "波增量"（fold(Empty, w1, w2, …)），使重放不会重复计数。
	// 例：Append 的 Empty 是 []any{}、Add 的是 0、Merge 的是 map[string]any{}、Last 无单位元故 nil。
	Empty any
}

// —— 四种常用通道的工厂；名字即语义。选哪个 = 声明"这个 key 遇到并发写怎么合"。——

// Last：last write wins——单值，如当前阶段、判定、最终答案。
// Empty=nil：last 无单位元，同波多写就是歧义，直接被 CheckAmbiguous 拦下。
func Last(init any) Channel {
	return Channel{Init: init, Reducer: lastReduce, AllowMulti: false, Empty: nil}
}

// Append：向列表追加——消息历史、检索到的片段、待办项。
// 初值总是非 nil 的 slice（对应 Python 的 list(init or [])），
// 因此没人写过的 append 通道读回 [] 而非 nil——下游 range 更安全。
// Empty=[]any{}：作为单位元，让一波内多次 append 先聚合再折入 shared。
func Append(init any) Channel {
	start := []any{}
	if s, ok := init.([]any); ok {
		start = append(start, s...)
	}
	return Channel{Init: start, Reducer: appendReduce, AllowMulti: true, Empty: []any{}}
}

// Add：数值累加——成本、token 计数、步数指标。
// init=nil 归零，避免 nil 参与加法；Empty=0 保证同波内先累再折。
func Add(init any) Channel {
	if init == nil {
		init = 0
	}
	return Channel{Init: init, Reducer: addReduce, AllowMulti: true, Empty: 0}
}

// Merge：map 合并——聚合多路结构化结果（各节点写自己那一段）。
// 拷贝 init 避免调用方持有的 map 被后续 Run 折入时污染。
// Empty=map[string]any{}：同波内多次写先合并成一个 map 再折入 shared。
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
// Writer 存在的唯一目的：给 CheckAmbiguous 判定"同波同 key 是否被多个不同节点写"用的。
type write struct {
	Key    string // 通道名（必须已在 channels 中声明）
	Value  any    // 增量值（不是最终值，最终值由 Reducer 现算）
	Writer string // 来源节点 key，用于歧义检测与 trace 归属
}

// WaveWrites 收集一波内每个节点的状态增量，在屏障处折叠一次。节点运行时绝不直接碰
// 共享状态，只把增量交到这里，消除读到别家半成品状态的竞态。
//
// 生命周期（每波新建一次）：Buffer × N → CheckAmbiguous → Drain → 上层用 Reducer 折叠。
type WaveWrites struct {
	channels map[string]Channel // 引用 Plan.Channels，用来校验 key 合法性 + 查 AllowMulti
	buffer   []write            // 按到达顺序排；Drain 后归 nil
}

// NewWaveWrites 创建绑定某 Plan 通道的空缓冲。
func NewWaveWrites(channels map[string]Channel) *WaveWrites {
	return &WaveWrites{channels: channels}
}

// Buffer 记录一次写。写未声明的通道几乎总是拼写错误或设计遗漏——尽早报错好过默默丢弃。
// 这里只"记账"不改 shared：真正的合并延后到屏障处，一次成一条 EvStateDelta fact。
func (w *WaveWrites) Buffer(key string, value any, writer string) error {
	if _, ok := w.channels[key]; !ok {
		return fmt.Errorf("wrote to undeclared state channel %q (from node %q)", key, writer)
	}
	w.buffer = append(w.buffer, write{Key: key, Value: value, Writer: writer})
	return nil
}

// CheckAmbiguous 在屏障处检查是否有 last 通道被多个节点写过。
// 只在 failed==nil 时由 drive 调用（见 scheduler.go 波结算分支）——一波失败的写不值得再做歧义诊断。
func (w *WaveWrites) CheckAmbiguous() error {
	// 第一遍：按 (channel key → 写过它的节点集合) 归并。
	// 用 map[string]bool 而非 []string 顺手去重：同一节点在同波写同 key 多次不算歧义。
	writersByKey := map[string]map[string]bool{}
	for _, wr := range w.buffer {
		if writersByKey[wr.Key] == nil {
			writersByKey[wr.Key] = map[string]bool{}
		}
		writersByKey[wr.Key][wr.Writer] = true
	}
	// 第二遍：对每个 key 查 AllowMulti，false 且写入者≥ 2 即报错。
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
// 调用方拿到 []write 后逐个过 channel.Reducer 折叠成"一个波增量"再写回 shared；
// Drain 后 w.buffer 归 nil —— 下一波新建一个 WaveWrites 从零开始。
func (w *WaveWrites) Drain() []write {
	out := w.buffer
	w.buffer = nil
	return out
}
