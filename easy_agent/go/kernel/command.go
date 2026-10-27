package kernel

// command —— 节点用来与 scheduler 对话的唯一控制词汇。
//
// 节点结束时，其 Outcome 可携带控制命令，告知
// scheduler 下一波如何进行。命令只描述意图；
// ready 集合由 scheduler 改变。只有两个，无隐藏词汇：
//
//   - 无命令（nil）：沿 Plan 中声明的静态边；
//   - Goto：重新 re-arm 某节点 —— 循环/回边（ReAct 如何循环）与
//     运行时跳转；可携带 payload 作为目标的下一次输入；
//   - Send：运行时实例化模板节点 —— 当分支数只能在运行时确定
//     时的 fan-out。
//
// 多 agent "transfer" 不是新命令：对另一 agent 节点的 Goto 且无返回边
// 意为"交接后不再返回"。
//
// 没有 Update 命令：把数据写入状态是 Outcome.StateDelta 的职责。
// 数据归数据，控制归控制。

// Command 是控制命令的标记接口。每个都是不可变事实；
// 只有 scheduler 会对它们做出反应。
type Command interface {
	isCommand()
}

// Goto 重新 re-arm Target（移回 PENDING），使其可再次运行。
//
// 重新 re-arm 后何时就绪有两种模式，由 Immediate 选择：
//   - Immediate=true（默认）：同时加入 release 列表，使下一波
//     绕过前驱立即就绪。用于
//     顺序回边（ReAct 循环）与运行时跳转。
//   - Immediate=false：只 re-arm；就绪仍由入边
//     与 join 决定。用于迭代收敛点（多轮黑板）。
//
// Payload 是本次转换交给目标的输入，与 Send.Payload 对称
// （多 agent 交接用它携带摘要）。
type Goto struct {
	Target    string
	Immediate bool
	Payload   any
}

func (Goto) isCommand() {}

// GotoNow 立即 re-arm 并 release（回边 / 跳转 / 交接）。
func GotoNow(target string, payload any) Goto {
	return Goto{Target: target, Immediate: true, Payload: payload}
}

// GotoRejoin 只 re-arm；像普通边一样等待本波的前驱/join。
func GotoRejoin(target string, payload any) Goto {
	return Goto{Target: target, Immediate: false, Payload: payload}
}

// Send 是动态 fan-out：实例化模板节点一次并喂入 Payload。
//
// Key 是实例标识；为空时 scheduler 按顺序编号。
// 相同 key 用于去重。
type Send struct {
	Template string
	Payload  any
	Key      string
}

func (Send) isCommand() {}
