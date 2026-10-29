package kernel

import (
	"fmt"
	"math"
	"sort"
	"sync"
)

// graph —— 静态蓝图：节点、边、Plan，以及"这一批该跑哪些节点"的计算。
//
// 蓝图不持有任何单次运行的动态数据 —— 那属于 Run。一个 Plan 可被多个并发 Run
// 复用。这里唯一略显"动态"的是 Ready()：给定 Run 当前状态，算出下一批可运行
// 节点。它是纯读取，不改动任何东西。

// EdgePredicate 只看当前共享状态，判定一条边是否"存活"。
type EdgePredicate func(shared map[string]any) bool

// Edge 是一条有向边。When == nil 表示恒存活。
type Edge struct {
	Source string
	Target string
	When   EdgePredicate
}

// RetryPolicy 是步骤级韧性（机制只负责执行；是否重试、退避多久是可替换的配置）。
type RetryPolicy struct {
	MaxAttempts int     // 最大尝试次数（含首次）
	BaseDelay   float64 // 秒；第 n 次失败后等待 BaseDelay*Factor^n
	Factor      float64
	RetryOn     func(error) bool // nil = 任何错误都重试
}

// DefaultRetryPolicy 与 Python 默认值一致（3 次尝试、50ms、x2）。
func DefaultRetryPolicy() *RetryPolicy {
	return &RetryPolicy{MaxAttempts: 3, BaseDelay: 0.05, Factor: 2.0}
}

// DelayFor 返回第 failedAttempt 次失败（0 起）后重试前的退避时长。
func (p *RetryPolicy) DelayFor(failedAttempt int) float64 {
	return p.BaseDelay * math.Pow(p.Factor, float64(failedAttempt))
}

// Matches 判断 err 在此策略下是否值得重试。
func (p *RetryPolicy) Matches(err error) bool {
	if p.RetryOn == nil {
		return true
	}
	return p.RetryOn(err)
}

// JoinSpec 为多前驱节点选择 join 策略。零值表示 all。
type JoinSpec struct {
	mode string // "", "all", "any"
	fn   func(done, total int) bool
}

// JoinAll：所有存活边完成时就绪（默认）。
func JoinAll() JoinSpec { return JoinSpec{mode: "all"} }

// JoinAny：任一存活边完成即就绪。
func JoinAny() JoinSpec { return JoinSpec{mode: "any"} }

// JoinQuorum 是自定义 quorum 策略的逃生口。
func JoinQuorum(fn func(done, total int) bool) JoinSpec { return JoinSpec{fn: fn} }

func (j JoinSpec) satisfied(done, total int) bool {
	if j.fn != nil {
		return j.fn(done, total)
	}
	if j.mode == "any" {
		return true
	}
	return done >= total
}

func (j JoinSpec) valid() bool {
	if j.fn != nil {
		return true
	}
	return j.mode == "" || j.mode == "all" || j.mode == "any"
}

// Node 是图中的一个步骤。
type Node struct {
	ID       string
	Body     NodeBody
	Join     JoinSpec
	Template bool         // 动态 fan-out 模板，经 Send 实例化
	Terminal bool         // 标记产出最终结果的收敛节点
	Timeout  float64      // 单次执行最大秒数；0 = 不超时
	Retry    *RetryPolicy // nil = 只跑一次，失败即失败
}

// Plan 是静态执行蓝图 = 节点表 + 边 + channel + 入口。
type Plan struct {
	Name     string
	Channels map[string]Channel
	Entry    []string

	mu        sync.Mutex // 防止并发自动声明时竞争 Channels
	nodes     map[string]*Node
	nodeOrder []string // 插入顺序 —— 迭代必须确定
	incoming  map[string][]Edge
	outgoing  map[string][]Edge
}

// NewPlan 构建空蓝图。
func NewPlan(name string, channels map[string]Channel) *Plan {
	if channels == nil {
		channels = map[string]Channel{}
	}
	return &Plan{
		Name:      name,
		Channels:  channels,
		nodes:     map[string]*Node{},
		incoming:  map[string][]Edge{},
		outgoing:  map[string][]Edge{},
		nodeOrder: nil,
	}
}

// —— 构建期：链式声明，建成后统一校验 ——

// Add 注册节点；id 重复即失败。
func (p *Plan) Add(nodes ...*Node) *Plan {
	for _, n := range nodes {
		if _, dup := p.nodes[n.ID]; dup {
			panic(fmt.Sprintf("duplicate node id: %s", n.ID))
		}
		p.nodes[n.ID] = n
		p.nodeOrder = append(p.nodeOrder, n.ID)
		if p.incoming[n.ID] == nil {
			p.incoming[n.ID] = []Edge{}
		}
		if p.outgoing[n.ID] == nil {
			p.outgoing[n.ID] = []Edge{}
		}
	}
	return p
}

// Edge 声明一条有向边，可按状态条件化。
func (p *Plan) Edge(source, target string, when EdgePredicate) *Plan {
	e := Edge{Source: source, Target: target, When: when}
	p.outgoing[source] = append(p.outgoing[source], e)
	p.incoming[target] = append(p.incoming[target], e)
	return p
}

// EnsureChannel 在 name 尚不存在时补声明一个 channel。可从并发节点 body 安全
// 调用（facade 会为它没被告知的状态键自动声明 channel）。
func (p *Plan) EnsureChannel(name string, ch Channel) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.Channels[name]; !ok {
		p.Channels[name] = ch
	}
}

// Validate 是编译期体检：悬空边、未知入口、非法 join 策略都在此失败，
// 而不是运行中途。检查顺序与 Python 原版一致（先边、再入口、后 join）。
func (p *Plan) Validate() error {
	// 边的源和目标都必须存在。outgoing 键按排序顺序迭代，报错因此确定。
	srcKeys := make([]string, 0, len(p.outgoing))
	for src := range p.outgoing {
		srcKeys = append(srcKeys, src)
	}
	sort.Strings(srcKeys)
	for _, src := range srcKeys {
		if _, ok := p.nodes[src]; !ok {
			return fmt.Errorf("edge source %q does not exist", src)
		}
		for _, e := range p.outgoing[src] {
			if _, ok := p.nodes[e.Target]; !ok {
				return fmt.Errorf("edge %s->%s target does not exist", src, e.Target)
			}
		}
	}
	for _, en := range p.Entry {
		if _, ok := p.nodes[en]; !ok {
			return fmt.Errorf("entry node %q does not exist", en)
		}
	}
	for _, id := range p.nodeOrder {
		if !p.nodes[id].Join.valid() {
			return fmt.Errorf("node %q has invalid join policy", id)
		}
	}
	if len(p.Entry) == 0 {
		// 无显式入口时，把没有入边的节点当作入口。
		for _, id := range p.nodeOrder {
			if len(p.incoming[id]) == 0 {
				p.Entry = append(p.Entry, id)
			}
		}
	}
	return nil
}

// —— 读访问 ——

// Nodes 返回节点表（只读使用）。
func (p *Plan) Nodes() map[string]*Node { return p.nodes }

// NodeOrder 按声明顺序返回节点 id。
func (p *Plan) NodeOrder() []string { return p.nodeOrder }

// Node 按 id 查找一个节点。
func (p *Plan) Node(id string) (*Node, bool) {
	n, ok := p.nodes[id]
	return n, ok
}

// Incoming 返回某节点入边的副本。
func (p *Plan) Incoming(nodeID string) []Edge {
	out := make([]Edge, len(p.incoming[nodeID]))
	copy(out, p.incoming[nodeID])
	return out
}

// InitialShared：每个 channel 从其声明的初始值开始。
func (p *Plan) InitialShared() map[string]any {
	shared := make(map[string]any, len(p.Channels))
	for k, ch := range p.Channels {
		shared[k] = ch.Init
	}
	return shared
}

// TerminalIDs 返回被标记的收敛节点；若无标记，则返回没有出边的节点。
func (p *Plan) TerminalIDs() []string {
	var marked []string
	for _, id := range p.nodeOrder {
		if p.nodes[id].Terminal {
			marked = append(marked, id)
		}
	}
	if len(marked) > 0 {
		return marked
	}
	var ends []string
	for _, id := range p.nodeOrder {
		if len(p.outgoing[id]) == 0 {
			ends = append(ends, id)
		}
	}
	return ends
}

// —— 键集合：静态（非模板）节点 + 全部 fan-out 实例 ——

func (p *Plan) staticKeys() []string {
	out := make([]string, 0, len(p.nodeOrder))
	for _, id := range p.nodeOrder {
		if !p.nodes[id].Template {
			out = append(out, id)
		}
	}
	return out
}

func (p *Plan) allKeys(run *Run) []string {
	out := p.staticKeys()
	for _, tmpl := range run.instanceOrder {
		out = append(out, run.Instances[tmpl]...)
	}
	return out
}

// —— 前驱检查 ——

func edgeLive(e Edge, shared map[string]any) bool {
	return e.When == nil || e.When(shared)
}

// instanceKeys 返回代表前驱源的键：节点自身，或模板的全部 fan-out 实例。
// 模板未产出实例时 ok 为 false。
func (p *Plan) instanceKeys(run *Run, source string) (keys []string, ok bool) {
	if !p.nodes[source].Template {
		return []string{source}, true
	}
	ks := run.Instances[source]
	if len(ks) == 0 {
		return nil, false
	}
	out := make([]string, len(ks))
	copy(out, ks)
	return out, true
}

// predecessorDone 判断前驱是否"成功完成"。
func (p *Plan) predecessorDone(run *Run, source string, emptyFanout bool, skipped map[string]bool) bool {
	if !p.nodes[source].Template {
		return run.IsCompleted(source) || skipped[source]
	}
	return p.predecessorTerminal(run, source, emptyFanout, skipped)
}

// predecessorTerminal 判断前驱是否到达任一终态。
func (p *Plan) predecessorTerminal(run *Run, source string, emptyFanout bool, skipped map[string]bool) bool {
	keys, ok := p.instanceKeys(run, source)
	if !ok {
		return emptyFanout
	}
	for _, k := range keys {
		if !(run.IsTerminal(k) || skipped[k]) {
			return false
		}
	}
	return true
}

// —— 核心：本波谁就绪 ——

// Ready 纯计算当前可运行的节点/实例 id。
//
// 条件边"此刻不存活"但其前驱尚未到终态时，只等待 —— 不提前判死（前驱下一波
// 可能改变状态；这正是 ReAct 回边）。真正的死分支由 SweepSkipped 在全图停滞后
// 清理。
func (p *Plan) Ready(run *Run, emptyFanout bool) []string {
	var ready []string
	for _, key := range p.allKeys(run) {
		if !run.IsPending(key) {
			continue
		}
		if run.IsInstance(key) { // Send 的拷贝：pending 即可运行
			ready = append(ready, key)
			continue
		}
		preds := p.incoming[key]
		// 起点：显式入口，或被 Goto 显式激活的节点。
		if containsStr(p.Entry, key) || run.Activated[key] {
			ready = append(ready, key)
			continue
		}
		if len(preds) == 0 {
			// 有显式入口时，孤立节点不自动启动。
			continue
		}
		hasOpenPred := false
		for _, e := range preds {
			if !p.predecessorTerminal(run, e.Source, emptyFanout, nil) {
				hasOpenPred = true
				break
			}
		}
		if hasOpenPred {
			continue // 前驱仍在运行，再等一波
		}
		var liveEdges []Edge
		for _, e := range preds {
			if edgeLive(e, run.Shared) {
				liveEdges = append(liveEdges, e)
			}
		}
		if len(liveEdges) == 0 {
			continue // 无存活边；图停滞后由 SweepSkipped 清理
		}
		done := 0
		for _, e := range liveEdges {
			if p.predecessorDone(run, e.Source, emptyFanout, nil) {
				done++
			}
		}
		if done == 0 {
			continue
		}
		if !p.nodes[key].Join.satisfied(done, len(liveEdges)) {
			continue
		}
		ready = append(ready, key)
	}
	return ready
}

// SweepSkipped 是死分支清理：前驱全部到终态、却没有存活边的 pending 静态节点，
// 级联至不动点。只在"本波无节点就绪"时调用，故不会误杀稍后可能被激活的分支。
// 纯函数：按序返回键，不触碰 Run。
func (p *Plan) SweepSkipped(run *Run) []string {
	var swept []string
	skipped := map[string]bool{}
	changed := true
	for changed {
		changed = false
		for _, key := range p.staticKeys() {
			if skipped[key] || !run.IsPending(key) || containsStr(p.Entry, key) {
				continue
			}
			preds := p.incoming[key]
			if len(preds) == 0 {
				continue
			}
			allTerminal := true
			for _, e := range preds {
				if !p.predecessorTerminal(run, e.Source, false, skipped) {
					allTerminal = false
					break
				}
			}
			anyLive := false
			for _, e := range preds {
				if p.predecessorDone(run, e.Source, false, skipped) && edgeLive(e, run.Shared) {
					anyLive = true
					break
				}
			}
			if allTerminal && !anyLive {
				skipped[key] = true
				swept = append(swept, key)
				changed = true
			}
		}
	}
	return swept
}

// IsDone 判断所有静态节点与动态实例是否都到达终态（done/skipped/failed）。
func (p *Plan) IsDone(run *Run) bool {
	for _, k := range p.allKeys(run) {
		if !run.IsTerminal(k) {
			return false
		}
	}
	return true
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
