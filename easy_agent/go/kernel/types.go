// Package kernel 是教学级 agent 内核：六个组件各司其职，
// 零第三方依赖。
//
// 阅读顺序即构造顺序：
//
//	types -> command -> channels   不可变值与状态合并规则
//	graph                        静态蓝图：nodes / edges / Plan
//	body                         单一可组合接口与四个 body
//	run                          一次动态执行：状态机 / apply
//	eventlog                     events 是事实之源
//	bus / ports                  对外观察/裁决与可替换端口（DIP）
//	scheduler                    汇集以上全部的波引擎
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
