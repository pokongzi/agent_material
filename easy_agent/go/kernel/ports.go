package kernel

// ports —— 内核与外部世界的边界（端口 / DIP）。
//
// 内核只知道这些接口。它不知道 OpenAI、向量库，或某工具是 HTTP 调用
// 还是本地函数。组合根注入具体实现；测试换入脚本化假件，使整条流水线
// 离线且确定性地运行。

// LlmReply 是一次模型调用的归一化结果。
type LlmReply struct {
	Text      string
	ToolCalls []ToolCall
	Tokens    int
}

// LlmOptions 携带一次 chat 调用的可选参数。
type LlmOptions struct {
	Tools   []map[string]any // function-calling schema（已是 OpenAI 形态）
	System  string           // system prompt（"" = 无）
	OnDelta func(text, kind string)
}

// LlmPort 是模型端口（DIP）：进 messages，出归一化 reply。厂商住在
// adapter 里。
type LlmPort interface {
	Chat(messages []map[string]any, opt LlmOptions) (LlmReply, error)
}

// ToolPort 是工具端口（DIP）：一次受治理的工具调用（校验/鉴权/执行
// 在实现上）。ctx 让工具激活子 agent（agent-as-tool）。
type ToolPort interface {
	Dispatch(call ToolCall, ctx *NodeContext) (ToolResult, error)
}

// ActivateOptions 携带子 Run 的初始状态与标识。
type ActivateOptions struct {
	Input map[string]any
	Llm   LlmPort
	Tools ToolPort
}

// ChildResult 是被激活子 Run 的如实终态报告。
type ChildResult struct {
	RunID    string
	State    RunState
	Output   any
	Shared   map[string]any
	Question string // 子 Run 挂起时设置
	Error    string // 子 Run 失败时设置
}

// SubagentPort 用同一内核递归运行子 Run（调用语义 —— 返回结果）。
// node_id 让激活方把委派事实归属到发起节点。
type SubagentPort interface {
	Activate(spec *Plan, task string, parentRun *Run, nodeID string, opt ActivateOptions) (*ChildResult, error)
}
