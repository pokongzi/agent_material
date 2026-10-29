package kernel

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// scheduler —— 内核的唯一引擎：反复计算"当前谁就绪"，逐波推进。
//
//	while running:
//	    ready   = plan.Ready(run)          1) 沿边看本波谁就绪
//	    results = run ready concurrently   2) 波内并发（有界），不直接改共享状态
//	    barrier: fold deltas / apply commands  3) 波末统一提交（fact 已持久化）
//
// 三个关键性质：
//   - 波是一致性边界：运行中节点只产出 Outcome，绝不直接写共享状态；一切由
//     reducer 在屏障处折叠，并发结果确定，屏障天然是提交点；
//   - 挂起即"放手"：节点请求 Interrupt 时，波让其余节点跑完后整体 park（其
//     fact 已在日志中持久化）；resume 时 park 节点带回填的外部值重跑；失败守
//     同一纪律 —— 波先结算，然后 Run 停止；
//   - 多 Agent 不添新引擎：SubPlanBody 经激活端口递归运行子 Run —— 仍在原地。
//
// Go 注记：Python 原版用 asyncio 重叠 I/O。这里每波在有界信号量下用真实
// goroutine 跑就绪节点；每次账本提交由 commitMu 串行化，故 body 虽并发执行，
// fact 流仍全序、可重放。

// SchedulerOptions 携带宿主装配与性能上限。
type SchedulerOptions struct {
	Llm         LlmPort
	Tools       ToolPort
	Bus         *Bus
	EventLog    EventLog
	Blobs       BlobStore
	MaxWaves    int // 空转兜底；0 -> 64
	Concurrency int // 单个 Run 同时运行的节点数上限；0 -> 8
}

// Scheduler 是波引擎，组装内核的其余所有部件。
type Scheduler struct {
	llm         LlmPort
	tools       ToolPort
	bus         *Bus
	eventlog    EventLog
	blobs       BlobStore
	maxWaves    int
	concurrency int
	subagent    SubagentPort

	// commitMu 串行化每次账本提交（append + apply + fire），使 fact 流在并发
	// body 与子 Run 之间保持全序。
	commitMu sync.Mutex
}

// NewScheduler 构建引擎，为零副作用的内存部件填默认值。
func NewScheduler(opt SchedulerOptions) *Scheduler {
	if opt.Bus == nil {
		opt.Bus = NewBus()
	}
	if opt.EventLog == nil {
		opt.EventLog = NewInMemoryEventLog()
	}
	if opt.Blobs == nil {
		opt.Blobs = NewInMemoryBlobStore()
	}
	if opt.MaxWaves == 0 {
		opt.MaxWaves = 64
	}
	if opt.Concurrency == 0 {
		opt.Concurrency = 8
	}
	s := &Scheduler{
		llm:         opt.Llm,
		tools:       opt.Tools,
		bus:         opt.Bus,
		eventlog:    opt.EventLog,
		blobs:       opt.Blobs,
		maxWaves:    opt.MaxWaves,
		concurrency: opt.Concurrency,
	}
	s.subagent = &InProcessActivator{scheduler: s}
	return s
}

// Bus 暴露引擎的 Bus，供调用方挂接观察者/检查器。
func (s *Scheduler) Bus() *Bus { return s.bus }

// EventLog 暴露引擎的账本（用于 replay / Trace 投影）。
func (s *Scheduler) EventLog() EventLog { return s.eventlog }

// Blobs 暴露引擎的制品存储。
func (s *Scheduler) Blobs() BlobStore { return s.blobs }

// —— 主公开入口 ——

// Run 启动 plan 的一次新 Run 并驱动至终态。返回的 error 只留给结构性故障
// （歧义写入、提交失败）；节点/图失败则作为已记录的 fact 落入 run.State。
func (s *Scheduler) Run(plan *Plan, task string, input map[string]any) (*Run, error) {
	run, err := StartRun(plan, RunOptions{Task: task, Seed: input})
	if err != nil {
		return nil, err
	}
	return run, s.drive(plan, run)
}

// Resume 从已记录的 fact 重建未完成的 Run，为每个 park 节点回填一个值后重新
// 驱动。日志是唯一的持久物 —— resume 是 (events, value) 的纯函数。
//
// value 通常就是单个 park 节点的裸载荷。若同一波 park 了多个节点，传一个键集合
// 与 park 集合完全一致的 map[string]any。llm/tools 把恢复的 Run 绑回其 Agent 身份。
//
// 波中途崩溃的 Run 也走这里：重放后状态为 RUNNING 且有在途节点，它们会被重新
// arm 再驱动 —— 至少一次，副作用可能重执行。已完成的 Run 拒绝：其 fact 已是历史。
func (s *Scheduler) Resume(plan *Plan, runID string, value any, llm LlmPort, tools ToolPort) (*Run, error) {
	events, err := s.eventlog.Events(runID)
	if err != nil {
		return nil, err
	}
	run, err := Replay(plan, events)
	if err != nil {
		return nil, err
	}
	run.Llm = llm
	run.Tools = tools
	if run.State != RunSuspended && run.State != RunRunning {
		return nil, fmt.Errorf("run %s is %s; only a suspended or crashed (running) run can resume", runID, run.State)
	}
	if run.State == RunRunning {
		// 崩溃续跑：重新 arm 在途节点 —— 这是派生的恢复适配（其 node_started
		// fact 已表明"曾在运行"），不产生新 fact。
		for key, st := range run.NodeStates {
			if st.Status == NodeRunning {
				run.Rearm(key, true)
			}
		}
		return run, s.drive(plan, run)
	}
	parked := make([]string, 0, len(run.Interrupts))
	for k := range run.Interrupts {
		parked = append(parked, k)
	}
	sort.Strings(parked) // 为 fact 保证确定顺序（apply 无论如何都会 re-arm 全部）
	var values map[string]any
	if vm, ok := value.(map[string]any); ok && sameKeySet(vm, parked) {
		values = vm
	} else if len(parked) == 1 {
		values = map[string]any{parked[0]: value}
	} else {
		return nil, fmt.Errorf("resume value(s) must cover every parked node: %v", parked)
	}
	nodes := make([]any, len(parked))
	for i, k := range parked {
		nodes[i] = k
	}
	if err := s.commit(run, EvResumed, map[string]any{"nodes": nodes, "values": values}); err != nil {
		return run, err
	}
	return run, s.drive(plan, run)
}

func sameKeySet(m map[string]any, keys []string) bool {
	if len(m) != len(keys) {
		return false
	}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}

// drive —— 引擎主循环：真正把一张图跑起来的地方。
//
// 一句话：反复做"算出这一批该跑哪些节点 → 让它们并发跑 → 在屏障处统一结算"，
// 直到没有节点可跑、被挂起（park）、或失败为止。每一"批"就是一个"波"。
//
// 完整流程：
//
//	① 首次进入：绑定模型/工具端口，写下 run_started 与开场状态（seed）两条事实；
//	② 主循环，只要 Run 还在 running：
//	   1) 算就绪：nextReady 找出本波可跑的节点，以及被结构性跳过的死分支；
//	   2) 波内并发：先单线程解析输入并写 node_started（保证事件流顺序确定），
//	      再把各节点的 body 丢到 goroutine 上并发执行；
//	   3) 屏障结算：等全部跑完，把结果按 失败 / 挂起 / 成功 分类，
//	      用 reducer 折叠状态增量，逐个写成事实；
//	   4) 收尾：处理挂起、失败、控制命令（Goto/Send），决定继续下一波还是退出。
//
// 关键纪律：body 并发执行时绝不直接改共享状态，所有变更都留到屏障处、
// 经由唯一的 commit → run.Apply 落地，因此同样的事件流必然得到同样的结果。
func (s *Scheduler) drive(plan *Plan, run *Run) error {
	// 引擎入口是唯一解析身份端口（DIP）的地方：显式绑定予以保留（子 Run 或
	// 恢复的 Run 上 Agent 自己的 model/registry）；未绑定的 Run 继承 Scheduler
	// 的默认端口。
	if run.Llm == nil {
		run.Llm = s.llm
	}
	if run.Tools == nil {
		run.Tools = s.tools
	}
	// 每个 Run 一个池：委派链永不等待自己的槽位
	sem := make(chan struct{}, s.concurrency)

	if run.EventSeq == 0 && run.State == RunRunning {
		// 由账本 fact 决定账本 fact：event_seq == 0 表示该事件流尚未开启。
		// depth 搭载在 fact 上，跨进程 resume 仍保有树形深度熔断。
		if err := s.commit(run, EvRunStarted, map[string]any{
			"task": run.Task, "name": run.Name(), "depth": run.Depth,
		}); err != nil {
			return err
		}
		if len(run.Seed) > 0 { // 开场状态更新也和别的更新一样是一条 fact
			seedWrites := NewWaveWrites(plan.Channels)
			for key, value := range run.Seed {
				if err := seedWrites.Buffer(key, value, "<seed>"); err != nil {
					return err
				}
			}
			delta := run.WaveDelta(seedWrites.Drain(), plan.Channels)
			run.Seed = map[string]any{}
			if len(delta) > 0 {
				if err := s.commit(run, EvStateDelta, map[string]any{"delta": delta}); err != nil {
					return err
				}
			}
		}
	}

	for run.Running() {
		// 1) 算就绪：找出本波能跑的节点（ready）和被跳过的死分支（swept）
		ready, swept := s.nextReady(plan, run)
		for _, key := range swept { // 每个被结构性跳过的分支都是一条 fact
			if err := s.commit(run, EvNodeSkipped, map[string]any{"node": key}); err != nil {
				return err
			}
		}
		if len(ready) == 0 {
			if len(swept) > 0 {
				continue // 新的 skip 可能放行一个 join —— 结算前先重算
			}
			return s.settle(plan, run)
		}

		if waves := run.incrMetric("waves", 1); waves > s.maxWaves {
			return s.commit(run, EvRunFailed, map[string]any{"reason": fmt.Sprintf(
				"exceeded max waves %d; suspected spin (check that a back-edge makes progress)", s.maxWaves)})
		}

		// 2) 波内并发。输入、resume 值和 NODE_STARTED fact 都在单线程屏障内
		// （按 ready 顺序）解析，fact 流因此保持确定；只有 body 在 goroutine 上
		// 重叠执行。节点不共享可变状态，各产出一个 Outcome。
		prep := make([]prepared, 0, len(ready))
		for _, key := range ready {
			node, _ := plan.Node(run.TemplateOf(key))
			resumeValue := run.ResumeValues[key] // 在 fact 消费它之前先窥取
			input := s.nodeInput(plan, run, key)
			if err := s.commit(run, EvNodeStarted, map[string]any{"node": key}); err != nil {
				return err
			}
			prep = append(prep, prepared{key: key, node: node, input: input, resumeValue: resumeValue})
		}
		results := make([]nodeResult, len(prep))
		var wg sync.WaitGroup
		for i := range prep {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				results[i] = s.execNode(plan, run, prep[i])
			}(i)
		}
		wg.Wait()

		// 3) 屏障：统一处理结果。失败是 fail-fast：停的是 Run 而非结算 ——
		// 每个已启动的节点照常结算。每个结算节点恰成一条 fact；打标由 apply
		// 完成（并在终态消费其 Goto 输入）。
		parked := map[string]*Interrupt{}
		var controls []controlEntry
		writes := NewWaveWrites(plan.Channels)
		var failed *waveFailure

		for _, res := range results {
			switch {
			case res.err != nil:
				errStr := res.err.Error()
				if err := s.commit(run, EvNodeFailed, map[string]any{"node": res.key, "error": errStr}); err != nil {
					return err
				}
				if failed == nil {
					failed = &waveFailure{key: res.key, reason: errStr}
				}
			case res.outcome.Suspend != nil:
				it := *res.outcome.Suspend
				it.NodeID = res.key
				parked[res.key] = &it
				// park 保留其 Goto 输入供重跑
			default:
				for k, v := range res.outcome.StateDelta {
					if err := writes.Buffer(k, v, res.key); err != nil {
						return err
					}
				}
				if len(res.outcome.Control) > 0 {
					controls = append(controls, controlEntry{writer: res.key, commands: res.outcome.Control})
				}
				if err := s.commit(run, EvNodeCompleted, map[string]any{"node": res.key, "output": res.outcome.Value}); err != nil {
					return err
				}
			}
		}

		if failed == nil {
			// 即使波已失败，成功部分照常折叠
			if err := writes.CheckAmbiguous(); err != nil {
				return err
			}
		}
		delta := run.WaveDelta(writes.Drain(), plan.Channels)
		if len(delta) > 0 {
			if err := s.commit(run, EvStateDelta, map[string]any{"delta": delta}); err != nil {
				return err
			}
		}

		if len(parked) > 0 { // 请求本身是一条 fact，无论 Run 随后 park 还是失败
			if err := s.commit(run, EvInterrupted, parkedFacts(parked)); err != nil {
				return err
			}
		}

		if failed != nil {
			return s.commit(run, EvRunFailed, map[string]any{"node": failed.key, "reason": failed.reason})
		}

		if writer, reason, bad := invalidControl(plan, controls); bad {
			// Run 级失败：写入节点本身已完成，只是发出了非法命令，
			// 故该事件不携带失败节点。
			return s.commit(run, EvRunFailed, map[string]any{"reason": fmt.Sprintf("%s (from node %q)", reason, writer)})
		}

		if err := s.applyControls(run, controls); err != nil {
			return err
		}

		if len(parked) > 0 {
			break // 挂起 fact 已持久化在日志中
		}
	}
	return nil
}

// nextReady 报告当前可运行的节点，以及新被扫为死分支的节点。清扫是纯的：
// 其标记只在调用方提交 node_skipped fact 时落地，故放行 join 的 skip 在下一轮
// 循环才显现。空 fan-out 最后收敛。
func (s *Scheduler) nextReady(plan *Plan, run *Run) (ready []string, swept []string) {
	ready = plan.Ready(run, false)
	if len(ready) > 0 {
		return ready, nil
	}
	swept = plan.SweepSkipped(run)
	return plan.Ready(run, true), swept
}

// —— 执行单个节点 ——

type prepared struct {
	key         string
	node        *Node
	input       any
	resumeValue any
}

type nodeResult struct {
	key     string
	outcome Outcome
	err     error
}

type waveFailure struct {
	key    string
	reason string
}

type controlEntry struct {
	writer   string
	commands []Command
}

// execNode 运行一个已准备节点的 body（NODE_STARTED 已在屏障提交），把
// outcome/error 交回屏障。
func (s *Scheduler) execNode(plan *Plan, run *Run, p prepared) (res nodeResult) {
	res.key = p.key
	// panic 的 body 在此被兜住并转为节点失败，一个坏节点拖不垮整波（或进程）。
	defer func() {
		if r := recover(); r != nil {
			res.outcome = Outcome{}
			res.err = fmt.Errorf("panic: %v", r)
		}
	}()
	ctx := &NodeContext{
		Run:         run,
		NodeID:      p.key,
		llm:         run.Llm, // 身份端口在创建/resume 时已解析一次
		tools:       run.Tools,
		subagent:    s.subagent,
		bus:         s.bus,
		blobs:       s.blobs,
		record:      func(kind string, data map[string]any) error { return s.commit(run, kind, data) },
		facts:       func() ([]Event, error) { return s.eventlog.Events(run.RunID) },
		ResumeValue: p.resumeValue,
	}
	outcome, err := s.runBody(p.node, p.input, ctx, run, p.key)
	if err != nil {
		res.err = err
		return res
	}
	res.outcome = outcome
	return res
}

// runBody 用"超时 + 退避重试"这层步骤级韧性包裹节点的 body。机制固定；
// 策略（试几次、等多久、哪些错误值得重试）挂在 Node 上，完全可替换。
func (s *Scheduler) runBody(node *Node, value any, ctx *NodeContext, run *Run, key string) (Outcome, error) {
	policy := node.Retry
	attempts := 1
	if policy != nil {
		attempts = policy.MaxAttempts
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		outcome, err := s.runOnce(node, value, ctx)
		if err == nil {
			return outcome, nil
		}
		lastErr = err
		canRetry := policy != nil && attempt+1 < attempts && policy.Matches(err)
		if !canRetry {
			return Outcome{}, err
		}
		backoff := policy.DelayFor(attempt)
		if cerr := s.commit(run, EvNodeRetry, map[string]any{
			"node": key, "attempt": attempt + 1, "error": err.Error(), "backoff": backoff,
		}); cerr != nil {
			return Outcome{}, cerr
		}
		time.Sleep(time.Duration(backoff * float64(time.Second)))
	}
	return Outcome{}, lastErr
}

// runOnce 执行一次 body，遵守节点超时。超时时放弃 body goroutine（同步 Go
// body 无法中途取消）；超时计为一次失败，由重试策略决定后续。
func (s *Scheduler) runOnce(node *Node, value any, ctx *NodeContext) (Outcome, error) {
	if node.Timeout <= 0 {
		return node.Body.Run(value, ctx)
	}
	type bodyRes struct {
		o Outcome
		e error
	}
	ch := make(chan bodyRes, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- bodyRes{Outcome{}, fmt.Errorf("panic: %v", r)}
			}
		}()
		o, e := node.Body.Run(value, ctx)
		ch <- bodyRes{o, e}
	}()
	select {
	case r := <-ch:
		return r.o, r.e
	case <-time.After(time.Duration(node.Timeout * float64(time.Second))):
		return Outcome{}, fmt.Errorf("node %q timed out after %vs", node.ID, node.Timeout)
	}
}

// nodeInput 解析本波交给节点的输入：Send 载荷、Goto 投递、Run 任务（源节点），
// 或聚合的上游输出。
func (s *Scheduler) nodeInput(plan *Plan, run *Run, key string) any {
	if run.IsInstance(key) {
		return run.InputOf(key)
	}
	if v, ok := run.Deliveries[key]; ok {
		// Goto 迁移携带的输入：此处读取，节点到终态时消费 —— park 时保留供重跑。
		return v
	}
	preds := plan.Incoming(key)
	if len(preds) == 0 {
		return run.Task
	}
	upstream := map[string]any{}
	for _, e := range preds {
		src := e.Source
		node, _ := plan.Node(src)
		if node.Template { // 模板前驱：聚合其所有实例的输出
			var vals []any
			for _, k := range run.Instances[src] {
				if run.IsCompleted(k) {
					vals = append(vals, run.StateOf(k).Output)
				}
			}
			upstream[src] = vals
		} else if run.IsCompleted(src) && edgeLive(e, run.Shared) {
			// 只沿"当前存活"的边取前驱输出。
			upstream[src] = run.StateOf(src).Output
		}
	}
	if len(upstream) == 1 {
		for _, v := range upstream {
			return v
		}
	}
	return upstream
}

// —— 控制命令：改变"下一波的 ready 集合" ——

func (s *Scheduler) applyControls(run *Run, controls []controlEntry) error {
	for _, c := range controls {
		for _, cmd := range c.commands {
			switch v := cmd.(type) {
			case Goto:
				// immediate：立即 re-arm + 放行；否则只 re-arm。
				// fact 负责陈述，apply 负责执行。
				data := map[string]any{"op": "goto", "target": v.Target, "immediate": v.Immediate}
				if v.Payload != nil {
					data["payload"] = v.Payload
				}
				if err := s.commit(run, EvControl, data); err != nil {
					return err
				}
			case Send:
				data := map[string]any{"op": "send", "template": v.Template, "payload": v.Payload}
				if v.Key != "" {
					data["key"] = v.Key
				}
				if err := s.commit(run, EvControl, data); err != nil {
					return err
				}
			default:
				return fmt.Errorf("unknown control command: %T", cmd)
			}
		}
	}
	return nil
}

// invalidControl 对照蓝图校验本波的命令，命令非法时返回肇事写入方与原因
// （ok=true）。
func invalidControl(plan *Plan, controls []controlEntry) (writer string, reason string, ok bool) {
	for _, c := range controls {
		for _, cmd := range c.commands {
			switch v := cmd.(type) {
			case Goto:
				target, exists := plan.Node(v.Target)
				if !exists {
					return c.writer, fmt.Sprintf("Goto target %q does not exist", v.Target), true
				}
				if target.Template {
					return c.writer, fmt.Sprintf("Goto target %q is a template node; use Send", v.Target), true
				}
			case Send:
				target, exists := plan.Node(v.Template)
				if !exists {
					return c.writer, fmt.Sprintf("Send template %q does not exist", v.Template), true
				}
				if !target.Template {
					return c.writer, fmt.Sprintf("Send target %q is not a template node", v.Template), true
				}
			}
		}
	}
	return "", "", false
}

func parkedFacts(parked map[string]*Interrupt) map[string]any {
	// 完整的 park fact（question/payload）：仅凭事件流即可重建挂起现场。
	m := make(map[string]any, len(parked))
	for key, it := range parked {
		m[key] = map[string]any{"kind": it.Kind, "question": it.Question, "payload": it.Payload}
	}
	return map[string]any{"parked": m}
}

// —— 结算 ——

func (s *Scheduler) settle(plan *Plan, run *Run) error {
	// 终态 fact 也进事件流：重放必须能看出 Run 是否结束、如何结束，
	// 而不只是过程。
	if plan.IsDone(run) {
		return s.commit(run, EvRunCompleted, map[string]any{"output": s.finalOutput(plan, run)})
	}
	var pending []string
	for k, st := range run.NodeStates {
		if st.Status != NodePending {
			continue
		}
		if node, ok := plan.Node(k); ok && node.Template {
			continue // 无实例的模板不算"未完成"
		}
		pending = append(pending, k)
	}
	sort.Strings(pending)
	return s.commit(run, EvRunFailed, map[string]any{"reason": fmt.Sprintf(
		"graph stalled: no ready node but unfinished nodes remain %v (an edge is likely mis-wired)", pending)})
}

func (s *Scheduler) finalOutput(plan *Plan, run *Run) any {
	// 只取真正完成的收敛节点；被条件边结构性跳过的分支不进最终结果。
	var terms []string
	for _, t := range plan.TerminalIDs() {
		if run.IsCompleted(t) {
			terms = append(terms, t)
		}
	}
	if len(terms) == 1 {
		return run.StateOf(terms[0]).Output
	}
	values := make(map[string]any, len(terms))
	for _, t := range terms {
		values[t] = run.StateOf(t).Output
	}
	return values
}

// —— 面向子 Agent 激活器的窄公开接缝 ——

// RecordDelegation 记录一条 DELEGATED fact，把 nodeID 连到其新子 Run ——
// 崩溃后重新挂接的锚点。这是可插拔 SubagentPort（如远程 A2A/RPC 激活器）
// 把该 fact 落到父 Run 账本的唯一公开途径。
func (s *Scheduler) RecordDelegation(parentRun *Run, childRunID, nodeID string) error {
	return s.commit(parentRun, EvDelegated, map[string]any{"node": nodeID, "child_run_id": childRunID})
}

func (s *Scheduler) recordDelegation(parentRun *Run, childRunID, nodeID string) error {
	return s.commit(parentRun, EvDelegated, map[string]any{"node": nodeID, "child_run_id": childRunID})
}

// —— commit：fact 诞生的唯一途径 ——

// commit 先持久追加一条 fact，再 apply，再广播。日志先于其投影，Run.Apply 是
// 唯一的变更路径 —— 重放侧跑的是同一个 apply，因此活跃 Run 与其重放在纪律上
// 不会分叉。由 commitMu 串行化，并发 body 与子 Run 产出全序流。
func (s *Scheduler) commit(run *Run, kind string, data map[string]any) error {
	s.commitMu.Lock()
	defer s.commitMu.Unlock()
	if data == nil {
		data = map[string]any{}
	}
	ev := NewEvent(run.EventSeq+1, run.RunID, kind, data, run.ParentID)
	if err := s.eventlog.Append(ev); err != nil {
		return err
	}
	if err := run.Apply(ev); err != nil {
		return err
	}
	s.bus.Fire(kind, map[string]any{"evt": ev})
	return nil
}

// —— 默认子 Agent 激活器 ——

// InProcessActivator 用同一 Scheduler 在进程内递归运行子 Plan（调用语义）。
// 远程实现（A2A、RPC）只需满足 SubagentPort，内核一行不改 —— "在哪运行"
// 藏在端口（DIP）之后（位置透明）。
type InProcessActivator struct {
	scheduler *Scheduler
}

func (a *InProcessActivator) Activate(spec *Plan, task string, parentRun *Run, nodeID string, opt ActivateOptions) (*ChildResult, error) {
	// 由父 Run 诞生：ChildOf 计算深度账目。调用方提供开场状态更新与显式身份；
	// drive 在入口解析未绑定的端口。
	child, err := ChildOf(parentRun, spec, RunOptions{
		Task:  task,
		Seed:  opt.Input,
		Llm:   opt.Llm,
		Tools: opt.Tools,
	})
	if err != nil {
		return nil, err // 例如深度熔断；结构性错误向上传递
	}
	// 委派 fact 落在父 Run 日志上：崩溃后子 Run 的 run_id 存于此，
	// 子 Run 可被重新挂接而非沦为孤儿
	if err := a.scheduler.recordDelegation(parentRun, child.RunID, nodeID); err != nil {
		return nil, err
	}
	if err := a.scheduler.drive(spec, child); err != nil {
		return nil, err
	}
	// 如实报告子 Run 的终态；含义由调用方裁决。
	result := &ChildResult{
		RunID:  child.RunID,
		State:  child.State,
		Output: child.FinalOutput,
		Shared: child.Shared,
	}
	if child.State == RunFailed {
		result.Error = fmt.Sprintf("%v", child.FinalOutput)
	}
	if child.State == RunSuspended {
		// 调用方也随之 park：透出子 Run 的 question，让父 Run 的 park 问同样的问题
		for _, it := range child.Interrupts {
			result.Question = it.Question
			break
		}
	}
	return result, nil
}
