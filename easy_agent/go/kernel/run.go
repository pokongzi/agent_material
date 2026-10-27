package kernel

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

// run —— 一次动态执行：节点运行时状态、Interrupt 令牌、Run 状态机，
// 以及 Apply —— 唯一的变更入口。
//
// Plan 是图纸；Run 是它的一次执行。状态转换有单一入口（transition），
// 非法跳转立即失败。账本状态只能通过应用事实（Run.Apply）改变 ——
// 绝不靠自律。

const maxRunDepth = 8

// NodeRuntimeState 是节点（或动态实例）在本 Run 内的运行时状态：
// 纯记录 —— 每次变更都经由 Run 的 mark/rearm 方法。
type NodeRuntimeState struct {
	Status   NodeStatus
	Output   any
	Attempts int // 已发起的执行次数
}

// Interrupt 是挂起令牌：在哪个节点、为何、向外部问什么。
type Interrupt struct {
	Kind     string // approval / input / external / delegation
	Payload  any
	Question string
	NodeID   string // 由 Run 在 park（挂起）时填入
}

// DepthError 是深度熔断：结构性防止循环委派使 Run 树只增不减。
type DepthError struct{ Msg string }

func (e *DepthError) Error() string { return e.Msg }

func newID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("run-%x", time.Now().UnixNano()&0xffffffff)
	}
	return "run-" + hex.EncodeToString(b)
}

func isInstanceKey(key string) bool { return strings.Contains(key, "#") }

// RunOptions 携带 Run 的出生参数（Go 无关键字实参）。
// Seed 是首次 drive 时折叠的初始状态更新。
type RunOptions struct {
	ParentID string
	Depth    int
	Task     string
	Seed     map[string]any
	Llm      LlmPort
	Tools    ToolPort
}

// Run 是 Plan 的一次执行实例。同一 Plan 可同时有多个 Run，
// 彼此互不干扰。
type Run struct {
	Plan     *Plan
	RunID    string
	ParentID string
	Depth    int
	Task     string
	// 端口绑定 = 本 Run 以谁的身份行事（某个 Agent 的 model/tools）。活连线，
	// 绝非事实。账本属于驱动它的 Scheduler。
	Llm   LlmPort
	Tools ToolPort
	Seed  map[string]any

	EventSeq int

	Shared         map[string]any
	NodeStates     map[string]*NodeRuntimeState
	Instances      map[string][]string // 模板 id -> 实例 keys
	instanceOrder  []string            // 模板插入顺序（确定性迭代）
	InstanceInputs map[string]any
	Deliveries     map[string]any // 交给目标的 Goto payload
	instanceSeq    int
	Activated      map[string]bool // 被 Goto 显式激活的节点

	State        RunState
	Interrupts   map[string]*Interrupt
	ResumeValues map[string]any
	FinalOutput  any
	Metrics      map[string]int

	metricsMu sync.Mutex
}

// NewRun 构造 Run 但不校验 plan（replay 用它：标识源自起始事实）。
func NewRun(plan *Plan, runID string, opt RunOptions) *Run {
	if runID == "" {
		runID = newID()
	}
	seed := opt.Seed
	if seed == nil {
		seed = map[string]any{}
	}
	r := &Run{
		Plan:           plan,
		RunID:          runID,
		ParentID:       opt.ParentID,
		Depth:          opt.Depth,
		Task:           opt.Task,
		Llm:            opt.Llm,
		Tools:          opt.Tools,
		Seed:           copyMap(seed),
		Shared:         plan.InitialShared(),
		NodeStates:     map[string]*NodeRuntimeState{},
		Instances:      map[string][]string{},
		InstanceInputs: map[string]any{},
		Deliveries:     map[string]any{},
		Activated:      map[string]bool{},
		State:          RunRunning,
		Interrupts:     map[string]*Interrupt{},
		ResumeValues:   map[string]any{},
		Metrics:        map[string]int{"waves": 0, "llm_calls": 0, "tool_calls": 0, "tokens": 0},
	}
	for _, id := range plan.NodeOrder() {
		r.NodeStates[id] = &NodeRuntimeState{Status: NodePending}
	}
	return r
}

// StartRun 开始 plan 的一次全新 Run（先校验蓝图）。
func StartRun(plan *Plan, opt RunOptions) (*Run, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	return NewRun(plan, "", opt), nil
}

// ChildOf 源自委派：depth 由父对象算出，因此树不变量
// （child = parent + 1）不会被遗忘或伪造。
func ChildOf(parent *Run, plan *Plan, opt RunOptions) (*Run, error) {
	depth := parent.Depth + 1
	if depth > maxRunDepth {
		return nil, &DepthError{Msg: fmt.Sprintf(
			"Run tree depth exceeds %d: check for a circular delegation between agents", maxRunDepth)}
	}
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	opt.ParentID = parent.RunID
	opt.Depth = depth
	return NewRun(plan, "", opt), nil
}

// Name 是蓝图的名字 —— 由 plan 派生的静态标识。
func (r *Run) Name() string { return r.Plan.Name }

// —— 状态机：单一转换入口 ——

func (r *Run) transition(target RunState) error {
	if !allowedTransitions[r.State][target] {
		return fmt.Errorf("illegal state transition: %s -> %s", r.State, target)
	}
	r.State = target
	return nil
}

// Complete 记录最终输出并转入 COMPLETED。
func (r *Run) Complete(output any) error {
	r.FinalOutput = output
	return r.transition(RunCompleted)
}

// Fail 记录原因并转入 FAILED。
func (r *Run) Fail(reason string) error {
	r.FinalOutput = reason
	return r.transition(RunFailed)
}

// Suspend 整体挂起：本波所有请求 park 的节点。
func (r *Run) Suspend(interrupts map[string]*Interrupt) error {
	r.Interrupts = interrupts
	return r.transition(RunSuspended)
}

// Resume 按每个 park 的节点 id 回喂一个外部值。
func (r *Run) Resume(values map[string]any) error {
	r.ResumeValues = copyMap(values)
	r.Interrupts = map[string]*Interrupt{}
	return r.transition(RunRunning)
}

// Running 报告 Run 是否处于 RUNNING 状态。
func (r *Run) Running() bool { return r.State == RunRunning }

// —— 节点状态查询 ——

// StateOf 返回节点的运行时状态（未知为 nil）。
func (r *Run) StateOf(key string) *NodeRuntimeState { return r.NodeStates[key] }

// IsInstance 报告 key 是否为活跃的动态（Send）实例。
func (r *Run) IsInstance(key string) bool {
	if !isInstanceKey(key) {
		return false
	}
	_, ok := r.InstanceInputs[key]
	return ok
}

// TemplateOf 去掉实例 key 的 "#suffix" 后缀。
func (r *Run) TemplateOf(key string) string {
	if isInstanceKey(key) {
		return strings.SplitN(key, "#", 2)[0]
	}
	return key
}

// IsPending 报告 key 是否存在且为 PENDING。
func (r *Run) IsPending(key string) bool {
	st := r.NodeStates[key]
	return st != nil && st.Status == NodePending
}

// IsCompleted 报告 key 是否为 COMPLETED。
func (r *Run) IsCompleted(key string) bool {
	st := r.NodeStates[key]
	return st != nil && st.Status == NodeCompleted
}

// IsTerminal 报告 key 是否到达 done/skipped/failed。
func (r *Run) IsTerminal(key string) bool {
	st := r.NodeStates[key]
	return st != nil && (st.Status == NodeCompleted || st.Status == NodeSkipped || st.Status == NodeFailed)
}

// —— 节点状态变更（单一入口；仅 Run.Apply 在 live 中调用它们）——

// MarkRunning 将节点置为 RUNNING，计数尝试次数，并消耗掉一次性
// 即时激活（使被 Goto 过的节点不会永久绕过门控）。
func (r *Run) MarkRunning(key string) {
	st := r.NodeStates[key]
	st.Status = NodeRunning
	st.Attempts++
	delete(r.Activated, key)
}

// MarkCompleted 结算节点的输出。
func (r *Run) MarkCompleted(key string, output any) {
	st := r.NodeStates[key]
	st.Status = NodeCompleted
	st.Output = output
}

// MarkSkipped 结构性跳过节点。
func (r *Run) MarkSkipped(key string) { r.NodeStates[key].Status = NodeSkipped }

// MarkFailed 记录节点失败。
func (r *Run) MarkFailed(key string, errMsg string) {
	st := r.NodeStates[key]
	st.Status = NodeFailed
	st.Output = errMsg
}

// Rearm 把（可能已 COMPLETED 的）节点移回 PENDING —— 唯一能让
// 节点再次运行的底层动作。immediate 还会把它加入 Activated，
// 使下一波绕过前驱立即就绪。
func (r *Run) Rearm(key string, immediate bool) {
	st := r.NodeStates[key]
	if st == nil {
		st = &NodeRuntimeState{Status: NodePending}
		r.NodeStates[key] = st
	}
	st.Status = NodePending
	if immediate {
		r.Activated[key] = true
	}
}

// —— 动态实例（Send fan-out）——

// AddInstance 实例化模板副本并返回其完整 key。
func (r *Run) AddInstance(template string, payload any, key string) string {
	r.instanceSeq++
	suffix := key
	if suffix == "" {
		suffix = fmt.Sprintf("%d", r.instanceSeq)
	}
	fullKey := template + "#" + suffix
	if _, exists := r.NodeStates[fullKey]; !exists {
		r.NodeStates[fullKey] = &NodeRuntimeState{Status: NodePending}
		r.InstanceInputs[fullKey] = payload
		if _, seen := r.Instances[template]; !seen {
			r.instanceOrder = append(r.instanceOrder, template)
		}
		r.Instances[template] = append(r.Instances[template], fullKey)
	}
	return fullKey
}

// InputOf 返回动态实例的 Send payload（静态节点为 nil）。
func (r *Run) InputOf(key string) any { return r.InstanceInputs[key] }

// —— 波屏障：将本波的增量聚合为一个事实 ——

// WaveDelta 将本波写入聚合成“波增量” —— 纯函数，不改动任何东西。
// 对同一 channel 的多次写入先从该 channel 的单位元折叠，
// 因此 replay 不会重复计数。
func (r *Run) WaveDelta(writes []write, channels map[string]Channel) map[string]any {
	out := map[string]any{}
	for _, w := range writes {
		ch := channels[w.Key]
		base, seen := out[w.Key]
		if !seen {
			base = ch.Empty
		}
		out[w.Key] = ch.Reducer(base, w.Value)
	}
	return out
}

// —— 指标（引擎遥测；绝非事实，针对波并发加锁）——

func (r *Run) incrMetric(key string, n int) int {
	r.metricsMu.Lock()
	defer r.metricsMu.Unlock()
	r.Metrics[key] += n
	return r.Metrics[key]
}

// MetricsSnapshot 返回遥测计数器的副本。
func (r *Run) MetricsSnapshot() map[string]int {
	r.metricsMu.Lock()
	defer r.metricsMu.Unlock()
	out := make(map[string]int, len(r.Metrics))
	for k, v := range r.Metrics {
		out[k] = v
	}
	return out
}

// —— 唯一的变更入口 ——

// Apply 是账本状态变更的唯一入口：把一条已记录的事实折叠进本 Run。
// live 调度经此提交、replay 经此重放，二者共用同一折叠，不会漂移。
func (r *Run) Apply(ev Event) error {
	if ev.RunID != r.RunID {
		return fmt.Errorf("fact belongs to run %q, not %q", ev.RunID, r.RunID)
	}
	d := ev.Data
	switch ev.Kind {
	case EvRunStarted:
		if r.EventSeq != 0 {
			return fmt.Errorf("run_started may only open a fresh stream")
		}
	case EvNodeStarted:
		node := dataStr(d, "node")
		if err := r.requireNode(node); err != nil {
			return err
		}
		r.MarkRunning(node)
		delete(r.ResumeValues, node) // 重跑消费掉它的答案
	case EvNodeCompleted:
		node := dataStr(d, "node")
		if err := r.requireNode(node); err != nil {
			return err
		}
		r.MarkCompleted(node, d["output"])
		delete(r.Deliveries, node) // Goto 输入在终态失效
	case EvNodeFailed:
		node := dataStr(d, "node")
		if err := r.requireNode(node); err != nil {
			return err
		}
		r.MarkFailed(node, dataStr(d, "error"))
		delete(r.Deliveries, node)
	case EvNodeSkipped:
		node := dataStr(d, "node")
		if err := r.requireNode(node); err != nil {
			return err
		}
		r.MarkSkipped(node)
	case EvStateDelta:
		ApplyEvent(r.Shared, ev, r.Plan.Channels)
	case EvControl:
		if dataStr(d, "op") == "goto" {
			target := dataStr(d, "target")
			immediate := true
			if v, ok := d["immediate"].(bool); ok {
				immediate = v
			}
			r.Rearm(target, immediate)
			if p, ok := d["payload"]; ok && p != nil {
				r.Deliveries[target] = p
			}
		} else { // "send"：实例化一份模板副本
			r.AddInstance(dataStr(d, "template"), d["payload"], dataStr(d, "key"))
		}
	case EvInterrupted:
		parked, _ := d["parked"].(map[string]any)
		interrupts := map[string]*Interrupt{}
		for node, raw := range parked {
			p, _ := raw.(map[string]any)
			kind := "external"
			if v, ok := p["kind"].(string); ok && v != "" {
				kind = v
			}
			question, _ := p["question"].(string)
			interrupts[node] = &Interrupt{Kind: kind, Payload: p["payload"], Question: question, NodeID: node}
		}
		if err := r.Suspend(interrupts); err != nil {
			return err
		}
	case EvResumed:
		values, _ := d["values"].(map[string]any)
		if err := r.Resume(values); err != nil {
			return err
		}
		if nodes, ok := d["nodes"].([]any); ok {
			for _, n := range nodes {
				if s, ok := n.(string); ok {
					r.Rearm(s, true)
				}
			}
		}
	case EvRunCompleted:
		if err := r.Complete(d["output"]); err != nil {
			return err
		}
	case EvRunFailed:
		if err := r.Fail(dataStr(d, "reason")); err != nil {
			return err
		}
	}
	r.EventSeq = ev.Seq
	return nil
}

// dataStr 从事件数据读取字符串字段（缺失或非字符串时为 ""）。
func dataStr(d map[string]any, key string) string {
	if s, ok := d[key].(string); ok {
		return s
	}
	return ""
}

// requireNode 在事实引用了本 Run 蓝图未知的节点时大声失败 —— 流属于
// 另一个 Plan（plan 漂移）。它是 Python 原版所抛 KeyError 的 Go 对应版本，
// 保留为可捕获的 error 而非 panic，使接错线的 resume 会报告而非崩溃。
func (r *Run) requireNode(key string) error {
	if _, ok := r.NodeStates[key]; !ok {
		return fmt.Errorf("fact references unknown node %q (the plan drifted from the recorded stream)", key)
	}
	return nil
}
