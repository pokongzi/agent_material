package kernel

import "fmt"

// body —— 唯一的可组合接口，外加四种内置 body。
//
// 在 kernel 眼里只有一种"可调度的东西"：满足 NodeBody 的可执行体，接收 input 与
// NodeContext 并返回 Outcome。所以普通函数、受治理的工具调用、固定 prompt 的模型
// 调用、激活子计划都是 body——多智能体无非是某个 body 递归运行另一张图。
//
// Outcome 正交地拆成四部分：Value（给下游）、StateDelta（经 reducer 折叠）、
// Control（Goto/Send）、Suspend（请求在此 park）。

// Outcome 是 body 的产出。
type Outcome struct {
	Value      any
	StateDelta map[string]any
	Control    []Command // nil = follow static edges naturally
	Suspend    *Interrupt
}

// OK 构造只携带下游值的 Outcome。
func OK(value any) Outcome { return Outcome{Value: value} }

// OKDelta 构造携带值加状态增量的 Outcome。
func OKDelta(value any, delta map[string]any) Outcome {
	return Outcome{Value: value, StateDelta: delta}
}

// GotoOutcome 转移到 target；payload 是其下一个输入，delta 折叠进状态。
// （与 Goto 命令类型区分命名；默认立即执行。）
func GotoOutcome(target string, payload any, delta map[string]any) Outcome {
	return Outcome{StateDelta: delta, Control: []Command{GotoNow(target, payload)}}
}

// SendOutcome 用 payload 喂入，实例化一份模板副本。
func SendOutcome(template string, payload any, key string) Outcome {
	return Outcome{Control: []Command{Send{Template: template, Payload: payload, Key: key}}}
}

// Park 请求在此挂起，交出控制权等待 resume。
func Park(kind string, payload any, question string) Outcome {
	return Outcome{Suspend: &Interrupt{Kind: kind, Payload: payload, Question: question}}
}

// CoerceOutcome 让 body 可以写得简洁：裸值/map/命令/组合会自动归一化为 Outcome。
func CoerceOutcome(raw any) Outcome {
	switch v := raw.(type) {
	case nil:
		return Outcome{}
	case Outcome:
		return v
	case *Outcome:
		if v == nil {
			return Outcome{}
		}
		return *v
	case Command:
		return Outcome{Control: []Command{v}}
	case []Command:
		return coerceList(toAnySlice(v))
	case []Outcome:
		return coerceList(outcomesToAny(v))
	case []any:
		return coerceList(v)
	case map[string]any:
		// 裸 map 折叠进状态；若 map 本身就是要传的值，请用 OK 包一层。
		return Outcome{StateDelta: v}
	default:
		return Outcome{Value: raw}
	}
}

func coerceList(list []any) Outcome {
	if len(list) == 0 {
		return Outcome{Value: list}
	}
	for _, x := range list {
		if !isCommandOrOutcome(x) {
			// 普通列表仍算业务值——不能仅因它是列表就猜成命令。
			return Outcome{Value: list}
		}
	}
	var controls []Command
	delta := map[string]any{}
	for _, item := range list {
		oc := CoerceOutcome(item)
		controls = append(controls, oc.Control...)
		for k, v := range oc.StateDelta {
			delta[k] = v
		}
	}
	out := Outcome{StateDelta: delta}
	if len(controls) > 0 {
		out.Control = controls
	}
	return out
}

func isCommandOrOutcome(x any) bool {
	switch x.(type) {
	case Command, Outcome, *Outcome:
		return true
	}
	return false
}

func toAnySlice(cmds []Command) []any {
	out := make([]any, len(cmds))
	for i, c := range cmds {
		out[i] = c
	}
	return out
}

func outcomesToAny(ocs []Outcome) []any {
	out := make([]any, len(ocs))
	for i, o := range ocs {
		out[i] = o
	}
	return out
}

// NodeBody 是唯一的可组合接口：input + context 进，Outcome 出。
type NodeBody interface {
	Run(input any, ctx *NodeContext) (Outcome, error)
}

// NodeContext 是 body 运行时可用的"服务接线"。它是接线而非数据：持有活对象
// （模型端口、工具端口），因此永远不会成为事实。一次 run 的数据经 input / StateDelta 传递。
type NodeContext struct {
	Run    *Run
	NodeID string

	llm      LlmPort
	tools    ToolPort
	subagent SubagentPort
	bus      *Bus
	blobs    BlobStore
	// 账本的写/读函数对，由 scheduler 注入，让 body 不持有日志也能对它读写。
	record      func(kind string, data map[string]any) error
	facts       func() ([]Event, error)
	ResumeValue any
}

// Shared 是共享状态的只读视图；要改状态请返回 StateDelta，由引擎在波屏障处折叠。
func (c *NodeContext) Shared() map[string]any { return c.Run.Shared }

// RunID 是当前 run 的 id。
func (c *NodeContext) RunID() string { return c.Run.RunID }

// Bus 是宿主 scheduler 的总线（只读接线）。
func (c *NodeContext) Bus() *Bus { return c.bus }

// Emit 向 bus 发射一个高频事件（不进事件日志）。
func (c *NodeContext) Emit(event string, data map[string]any) {
	if c.bus == nil {
		return
	}
	payload := map[string]any{"run_id": c.RunID(), "node_id": c.NodeID}
	for k, v := range data {
		payload[k] = v
	}
	c.bus.Fire(event, payload)
}

// LlmComplete 发起一次固定 prompt 的模型调用：它处理 input，不决定流程。
func (c *NodeContext) LlmComplete(prompt, system string) (string, error) {
	reply, err := c.LlmChat([]map[string]any{{"role": "user", "content": prompt}}, LlmOptions{System: system})
	if err != nil {
		return "", err
	}
	return reply.Text, nil
}

// LlmChat 经模型端口发起一次 chat 调用，并统一计量。
func (c *NodeContext) LlmChat(messages []map[string]any, opt LlmOptions) (LlmReply, error) {
	if c.llm == nil {
		return LlmReply{}, fmt.Errorf("no LlmPort injected; cannot call the model")
	}
	reply, err := c.llm.Chat(messages, opt)
	if err != nil {
		return reply, err
	}
	c.Run.incrMetric("llm_calls", 1)
	c.Run.incrMetric("tokens", reply.Tokens)
	return reply, nil
}

// CallTool 经工具端口发起一次受治理的调用。返回的 error 只留给结构性（硬）失败；
// 普通工具失败以 ToolResult（OK=false）返回。
func (c *NodeContext) CallTool(name string, arguments map[string]any) (ToolResult, error) {
	if c.tools == nil {
		return ToolResult{}, fmt.Errorf("no ToolPort injected; cannot call a tool")
	}
	if arguments == nil {
		arguments = map[string]any{}
	}
	count := c.Run.incrMetric("tool_calls", 1)
	call := ToolCall{Name: name, Arguments: arguments, CallID: fmt.Sprintf("%s:%s:%d", c.RunID(), c.NodeID, count)}
	return c.tools.Dispatch(call, c)
}

// Spawn 激活一个子 Run（调用语义：完成后返回其结果）。
func (c *NodeContext) Spawn(spec *Plan, task string, opt ActivateOptions) (*ChildResult, error) {
	if c.subagent == nil {
		return nil, fmt.Errorf("no SubagentPort injected; cannot activate a sub-agent")
	}
	return c.subagent.Activate(spec, task, c.Run, c.NodeID, opt)
}

// SaveArtifact 持久化本次 run 产出的带版本文件：字节存入 BlobStore，事件日志只记一条
// 指针事实。返回该指针。
func (c *NodeContext) SaveArtifact(filename string, data any, mime, title string) (map[string]any, error) {
	if c.blobs == nil {
		return nil, fmt.Errorf("no BlobStore injected; cannot save an artifact")
	}
	if c.record == nil {
		return nil, fmt.Errorf("no event recorder injected; cannot record an artifact fact")
	}
	pointer, err := c.blobs.Save(c.RunID(), filename, data, mime)
	if err != nil {
		return nil, err
	}
	fact := copyMap(pointer)
	if title != "" {
		fact["title"] = title
	}
	if err := c.record(EvArtifactWritten, fact); err != nil {
		return nil, err
	}
	return pointer, nil
}

// LoadArtifact 读回 SaveArtifact 写入的内容。version <= 0 表示最新。
func (c *NodeContext) LoadArtifact(filename string, version int) ([]byte, error) {
	if c.blobs == nil {
		return nil, fmt.Errorf("no BlobStore injected; cannot load an artifact")
	}
	if c.facts == nil {
		return nil, fmt.Errorf("no ledger reader injected; cannot load an artifact")
	}
	events, err := c.facts()
	if err != nil {
		return nil, err
	}
	pointer, err := ResolveArtifact(events, filename, version)
	if err != nil {
		return nil, err
	}
	uri, _ := pointer["uri"].(string)
	return c.blobs.Load(uri)
}

// ════════════ 子 Run 判定（所有委托面共用一张表） ════════════

// ChildVerdict 把激活结果归约为三种终态判定之一。
type ChildVerdict struct {
	RunID    string
	State    RunState
	Output   any
	Question string
	Error    string
}

// ClassifyChildResult 把激活结果归一化为 ChildVerdict——所有委托面背后唯一的决策表，
// 使它们不会各自漂移。
func ClassifyChildResult(result *ChildResult) ChildVerdict {
	switch result.State {
	case RunSuspended:
		return ChildVerdict{RunID: result.RunID, State: RunSuspended, Question: result.Question}
	case RunFailed:
		e := result.Error
		if e == "" {
			if s, ok := result.Output.(string); ok {
				e = s
			}
		}
		return ChildVerdict{RunID: result.RunID, State: RunFailed, Error: e}
	default:
		return ChildVerdict{RunID: result.RunID, State: RunCompleted, Output: result.Output}
	}
}

// ════════════ 四种内置 body ════════════

// Fn 是普通函数 body 的签名。
type Fn func(input any, ctx *NodeContext) (any, error)

// FnBody（L0）适配一个 Go 函数；其裸返回值被归一化为 Outcome。
type FnBody struct{ Fn Fn }

// NewFnBody 把函数包装成 body。
func NewFnBody(fn Fn) *FnBody { return &FnBody{Fn: fn} }

func (b *FnBody) Run(input any, ctx *NodeContext) (Outcome, error) {
	raw, err := b.Fn(input, ctx)
	if err != nil {
		return Outcome{}, err
	}
	return CoerceOutcome(raw), nil
}

// ToolBody（L1）按名字发起一次受治理的工具调用；input 就是参数。
type ToolBody struct{ ToolName string }

func (b *ToolBody) Run(input any, ctx *NodeContext) (Outcome, error) {
	args, _ := input.(map[string]any)
	result, err := ctx.CallTool(b.ToolName, args)
	if err != nil {
		return Outcome{}, err
	}
	// 工具的成功/失败都作为值返回；下一步由模型决定。
	return OK(result), nil
}

// LLMBody（L2）发起一次固定 prompt 的模型调用。Prompt 可以是 string 或
// func(any) string。
type LLMBody struct {
	Prompt any
	System string
}

func (b *LLMBody) Run(input any, ctx *NodeContext) (Outcome, error) {
	var text string
	switch p := b.Prompt.(type) {
	case string:
		text = p
	case func(any) string:
		text = p(input)
	default:
		text = toTaskString(input)
	}
	answer, err := ctx.LlmComplete(text, b.System)
	if err != nil {
		return Outcome{}, err
	}
	return OK(answer), nil
}

// SubPlanBody（L3）激活子计划/子智能体，用同一个 kernel 递归运行它，并折叠其终态。
type SubPlanBody struct {
	Spec  *Plan
	Llm   LlmPort
	Tools ToolPort
	// Seed 把节点的 task 映射为子 Plan 的开局状态更新。
	Seed func(task string) map[string]any
}

func (b *SubPlanBody) Run(input any, ctx *NodeContext) (Outcome, error) {
	if ctx.ResumeValue != nil {
		// 两段式 resume：子 Run 已被单独 resume，其输出作为本节点的 resume 值到达；
		// 重新 Spawn 会把子 Run 变成孤儿。
		return OK(ctx.ResumeValue), nil
	}
	task := toTaskString(input)
	var seed map[string]any
	if b.Seed != nil {
		seed = b.Seed(task)
	}
	result, err := ctx.Spawn(b.Spec, task, ActivateOptions{Input: seed, Llm: b.Llm, Tools: b.Tools})
	if err != nil {
		return Outcome{}, err // 如深度熔断；按结构性错误传播
	}
	verdict := ClassifyChildResult(result)
	if verdict.State == RunSuspended {
		// 子 Run 被 park 时调用者也一并 park：question 向上传，payload 携带子 Run 的
		// run_id，供 resume 找回它。
		return Park("delegation", map[string]any{"child_run_id": verdict.RunID, "task": task}, verdict.Question), nil
	}
	if verdict.State == RunFailed {
		// 调用语义：子 Run 失败即本节点失败。
		return Outcome{}, fmt.Errorf("%s", verdict.Error)
	}
	return OK(verdict.Output), nil
}

func toTaskString(input any) string {
	if input == nil {
		return ""
	}
	if s, ok := input.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", input)
}
