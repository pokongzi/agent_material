package kernel

import "fmt"

// replay —— 从记录的 Event 流反向投影出 Run。
//
// 它是同一批事实的第二个读取者，绝不是第二个调度器：identity 诞生于首个事实，
// 此后每个事实都走 Run.Apply —— 与 live 调度提交时同一个唯一变更入口。
// 这里没有别的东西可以漂移：
//
//	no Ready(), no body, no LLM, no tool — construct + for ev: run.Apply(ev)
//
// 事件流能复现：shared state、每个 Node 的 status/output/attempts、动态实例
// 及其输入、Goto 投递、挂起（含其提问）、resume 回填的答案，以及 Run 如何结束。
// 刻意不伪造两样东西：
//
//   - 粗粒度计数（waves / llm / tool / tokens）是引擎遥测而非事实：
//     重放出的 Run 让它们保持默认值；
//   - 崩溃后的"从这里继续"正是本函数的职责 —— Scheduler.Resume 重放事件流并
//     重新驱动，所以恢复动词只有一个，不是两个。

// Replay 通过折叠 Event 流重建一个 Run。plan 是产出该 Run 的同一张蓝图
// （其静态 nodes/edges/channels 不在日志里）。
func Replay(plan *Plan, events []Event) (*Run, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	var opened *Event
	for i := range events {
		if events[i].Kind == EvRunStarted {
			opened = &events[i]
			break
		}
	}
	if opened == nil {
		return nil, fmt.Errorf("cannot replay: the event stream has no run_started event")
	}
	// identity 诞生于首个事实（run_id/parent/depth/task）；开局 state 已是
	// 事件流中的 state_delta 事件，无需二次播种。
	run := NewRun(plan, opened.RunID, RunOptions{
		ParentID: opened.ParentID,
		Depth:    toInt(opened.Data["depth"]),
		Task:     dataStr(opened.Data, "task"),
	})
	for _, ev := range events {
		if err := run.Apply(ev); err != nil { // 唯一入口：与 live 提交同一路径
			return nil, err
		}
	}
	return run, nil
}
