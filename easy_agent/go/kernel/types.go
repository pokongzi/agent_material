// Package kernel 是教学级 agent 内核：零第三方依赖。
//
// 先记住一个三句话的故事，所有文件都是故事里的一个角色：
//
//  1. Plan 是图纸 —— 声明有哪些节点、谁连向谁；
//  2. Scheduler 是引擎 —— 一批一批地跑节点（"波"）；
//  3. 跑过的每一步都记成不可篡改的 Event —— 状态、轨迹、
//     制品全部能从事件流重算出来。
//
// 按"回答一个问题"的顺序读，每个文件解决一件事：
//
//	graph.go      图纸长什么样？（节点 / 边 / Plan）
//	body.go       节点里跑的代码长什么样？（统一接口 + 四种内置实现）
//	scheduler.go  谁来推进执行？（波引擎：算就绪 → 并发跑 → 屏障结算）
//	command.go    节点如何影响流程？（只有 Goto / Send 两个命令）
//	channels.go   并发写同一状态怎么办？（reducer 在波末合并）
//	run.go        执行进度记在哪？（状态机；Apply 是唯一写入口）
//	eventlog.go   历史记在哪？（Event 是事实之源）
//	bus.go        外界如何观察 / 拦截？（Fire 观察、Check 裁决、Subscribe 订阅）
//	ports.go      模型和工具从哪来？（接口边界，外部注入实现）
//	replay / trace / blob   同一批事件的三种读法（重建 / 画树 / 取文件）
//
//	types.go（本文件）是公共值类型，util.go 是数值小工具，遇到再查即可。
//
// 内核不引入任何模型厂商或具体工具实现。
package kernel

// NodeStatus 表示图中某个（执行实例的）节点的状态。
type NodeStatus string

const (
	NodePending   NodeStatus = "pending"   // 尚未就绪
	NodeRunning   NodeStatus = "running"   // 在当前波中运行
	NodeCompleted NodeStatus = "completed" // 成功完成，输出已结算
	NodeSkipped   NodeStatus = "skipped"   // 未命中任何条件边
	NodeFailed    NodeStatus = "failed"    // 执行失败
)

// RunState —— Run 生命周期内恰有四个状态。
type RunState string

const (
	RunRunning   RunState = "running"
	RunSuspended RunState = "suspended" // 已释放并持久化，等待人工/外部
	RunCompleted RunState = "completed"
	RunFailed    RunState = "failed"
)

// allowedTransitions 是唯一的合法转换表。任何表外跳转都会失败 ——
// “让非法状态不可表示”胜过事后检查。
var allowedTransitions = map[RunState]map[RunState]bool{
	RunRunning:   {RunSuspended: true, RunCompleted: true, RunFailed: true},
	RunSuspended: {RunRunning: true, RunFailed: true},
	RunCompleted: {},
	RunFailed:    {},
}

// ToolCall 是单次工具调用请求。
//
// CallID 在 Run 内每次调用唯一（调用计数器）；它在重试或挂起间并不
// 稳定 —— 重新投递可能重复执行副作用。
type ToolCall struct {
	Name      string
	Arguments map[string]any
	CallID    string
}

// ToolResult 是工具调用的结果。OK 为 false 时，Error 说明原因。
type ToolResult struct {
	OK     bool
	Output any
	Error  string
	CallID string
}

// ToolSuccess 构造成功的工具结果。
func ToolSuccess(output any, callID string) ToolResult {
	return ToolResult{OK: true, Output: output, CallID: callID}
}

// ToolFailure 构造失败的工具结果。
func ToolFailure(err string, callID string) ToolResult {
	return ToolResult{OK: false, Error: err, CallID: callID}
}
