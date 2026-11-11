package kernel

// 波结算定律：波同样是失败的一致性边界，委派永不自我等待，Goto 输入在 park 后存续，
// 终态持久。（test_wave_settlement.py 内核部分用例的 Go 对应版本。）

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// firstIndexOf 返回 kind 在 kinds 中的位置，缺失时返回 -1。
func firstIndexOf(kinds []string, kind string) int {
	for i, k := range kinds {
		if k == kind {
			return i
		}
	}
	return -1
}

// chainPlan 构建深度嵌套的委派链：每层 spawn 下一层，concurrency=1 时调度器全局信号量会死锁
// （父级持着唯一槽位等子级），而按 Run 独立的池不会。
func chainPlan(depth int) *Plan {
	p := NewPlan("", nil)
	if depth == 0 {
		p.Add(&Node{ID: "leaf", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			return OK("done"), nil
		})})
		return p
	}
	p.Add(&Node{ID: fmt.Sprintf("n%d", depth), Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
		result, err := ctx.Spawn(chainPlan(depth-1), "go deeper", ActivateOptions{})
		if err != nil {
			return nil, err
		}
		return OK(result.Output), nil
	})})
	return p
}

// TestDeepDelegationNeverWaitsOnItself 覆盖场景：深度嵌套的子 Agent 委派链
// （chainPlan(3)，每层 Spawn 下一层）在 concurrency=1（全局仅一个并发槽位）下仍能跑完，
// 不会自我死锁——因为每个 Run 用独立的信号量池，父级不会占着唯一槽位等子级。
// 断言：5 秒内返回、终态 RunCompleted、最终输出为叶子的 "done"；超时即判死锁失败。
func TestDeepDelegationNeverWaitsOnItself(t *testing.T) {
	sch := NewScheduler(SchedulerOptions{Concurrency: 1})
	type runRes struct {
		run *Run
		err error
	}
	done := make(chan runRes, 1)
	go func() {
		run, err := sch.Run(chainPlan(3), "", nil)
		done <- runRes{run, err}
	}()
	select {
	case res := <-done:
		if res.err != nil {
			t.Fatal(res.err)
		}
		if res.run.State != RunCompleted {
			t.Fatalf("state = %q, want completed", res.run.State)
		}
		if res.run.FinalOutput != "done" {
			t.Fatalf("final = %v, want done", res.run.FinalOutput)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("deep delegation deadlocked: a chain of nested sub-Runs must finish at concurrency=1")
	}
}

// TestAFailedWaveStillSettlesItsSiblings 覆盖场景：波是失败的一致性边界——fail-fast 只停 Run、
// 不停本波结算。同一波里 boom 节点失败、ok 兄弟节点成功时，成功部分照常折叠并落账。
// 断言：Run 终态 RunFailed；ok 的 delta 折叠进 Shared["kept"]="yes"；事件流含 NODE_COMPLETED
// 与 STATE_DELTA；且 NODE_FAILED 排在 RUN_FAILED 之前（先记节点失败，再判 Run 失败）。
func TestAFailedWaveStillSettlesItsSiblings(t *testing.T) {
	p := NewPlan("", map[string]Channel{"kept": Last(nil)})
	p.Add(
		&Node{ID: "boom", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			return nil, fmt.Errorf("boom")
		})},
		&Node{ID: "ok", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			return OKDelta(nil, map[string]any{"kept": "yes"}), nil
		})},
	)
	sch := NewScheduler(SchedulerOptions{})
	run, err := sch.Run(p, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunFailed {
		t.Fatalf("state = %q, want failed", run.State)
	}
	if run.Shared["kept"] != "yes" { // 兄弟 Node 的 delta 已折叠
		t.Fatalf("kept = %v, want yes", run.Shared["kept"])
	}
	kinds := eventKinds(t, sch, run.RunID)
	if firstIndexOf(kinds, EvNodeCompleted) < 0 {
		t.Fatalf("kinds = %v, want a NODE_COMPLETED", kinds)
	}
	if firstIndexOf(kinds, EvStateDelta) < 0 {
		t.Fatalf("kinds = %v, want a STATE_DELTA", kinds)
	}
	if firstIndexOf(kinds, EvNodeFailed) >= firstIndexOf(kinds, EvRunFailed) {
		t.Fatalf("kinds = %v, want NODE_FAILED before RUN_FAILED", kinds)
	}
}

// TestFailureWinsOverASameWaveParkButTheAskIsLogged 覆盖场景：同一波里 ask 节点 park、boom 兄弟
// 节点失败时，失败优先——Run 判失败而非挂起；但 park 的提问作为"已发生的事实"仍须落账。
// 断言：Run 终态 RunFailed；INTERRUPTED 排在 RUN_FAILED 之前且携带 question="approve?"；
// RUN_FAILED 是事件流最后一条；此后 Resume 必失败（终态是历史，不可恢复）。
func TestFailureWinsOverASameWaveParkButTheAskIsLogged(t *testing.T) {
	p := NewPlan("", nil)
	p.Add(
		&Node{ID: "ask", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			if ctx.ResumeValue == nil {
				return Park("approval", nil, "approve?"), nil
			}
			return OK(ctx.ResumeValue), nil
		})},
		&Node{ID: "boom", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			return nil, fmt.Errorf("boom")
		})},
	)
	sch := NewScheduler(SchedulerOptions{})
	run, err := sch.Run(p, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunFailed {
		t.Fatalf("state = %q, want failed", run.State)
	}

	events, _ := sch.EventLog().Events(run.RunID)
	kinds := eventKinds(t, sch, run.RunID)
	if firstIndexOf(kinds, EvInterrupted) >= firstIndexOf(kinds, EvRunFailed) {
		t.Fatalf("kinds = %v, want INTERRUPTED before RUN_FAILED", kinds)
	}
	var parked map[string]any
	for _, e := range events {
		if e.Kind == EvInterrupted {
			parked, _ = e.Data["parked"].(map[string]any)
		}
	}
	ask, _ := parked["ask"].(map[string]any)
	if ask == nil || ask["question"] != "approve?" {
		t.Fatalf("parked ask = %v, want question approve?", parked["ask"])
	}
	if kinds[len(kinds)-1] != EvRunFailed { // 终态事实为事件流收尾
		t.Fatalf("last kind = %q, want RUN_FAILED", kinds[len(kinds)-1])
	}
	if _, err := sch.Resume(p, run.RunID, "y", nil, nil); err == nil {
		t.Fatal("history, not something to resume")
	}
}

// TestAGotoPayloadSurvivesAPark 覆盖场景：Goto 携带的输入（Deliveries）由节点终态消费、而非读取即消费，
// 因此 park 的节点在 resume 后仍以同一份输入重跑（对应 scheduler.go "park 保留其 Goto 输入供重跑"）。
// 断言：首跑 worker 收到 "hand-off brief" 后 park（RunSuspended）；Resume 后 worker 以同样的
// "hand-off brief" 再跑一次并完成（RunCompleted），inputs 记录到两次且都为该 payload。
func TestAGotoPayloadSurvivesAPark(t *testing.T) {
	var mu sync.Mutex
	var inputs []any

	p := NewPlan("", nil)
	p.Add(
		&Node{ID: "start", Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			return GotoOutcome("worker", "hand-off brief", nil), nil
		})},
		&Node{ID: "worker", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			mu.Lock()
			inputs = append(inputs, x)
			mu.Unlock()
			if ctx.ResumeValue == nil {
				return Park("input", nil, "more info?"), nil
			}
			return OK(x), nil
		})},
	)
	p.Edge("start", "worker", nil)

	sch := NewScheduler(SchedulerOptions{})
	run, err := sch.Run(p, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunSuspended {
		t.Fatalf("state = %q, want suspended", run.State)
	}
	mu.Lock()
	first := append([]any(nil), inputs...)
	mu.Unlock()
	if len(first) != 1 || first[0] != "hand-off brief" {
		t.Fatalf("inputs = %v, want [hand-off brief]", first)
	}

	resumed, err := sch.Resume(p, run.RunID, "answer", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.State != RunCompleted {
		t.Fatalf("resumed state = %q, want completed", resumed.State)
	}
	mu.Lock()
	both := append([]any(nil), inputs...)
	mu.Unlock()
	if len(both) != 2 || both[0] != "hand-off brief" || both[1] != "hand-off brief" {
		t.Fatalf("inputs = %v, want [hand-off brief hand-off brief]", both)
	}
}

// TestTerminalStatesCloseTheStream 覆盖场景：终态持久——Run 结束后其结局是日志里不可变的事实，
// 事件流以终态事实收尾，不会有任何东西停留在 RUNNING。表驱动覆盖成功与失败两种收场。
// 断言：ok body → RunCompleted 且最后一条事件为 RUN_COMPLETED；
// boom body → RunFailed 且最后一条事件为 RUN_FAILED。
func TestTerminalStatesCloseTheStream(t *testing.T) {
	okBody := NewFnBody(func(x any, ctx *NodeContext) (any, error) { return "fine", nil })
	boomBody := NewFnBody(func(x any, ctx *NodeContext) (any, error) { return nil, fmt.Errorf("boom") })
	cases := []struct {
		body  *FnBody
		state RunState
		kind  string
	}{
		{okBody, RunCompleted, EvRunCompleted},
		{boomBody, RunFailed, EvRunFailed},
	}
	for _, tc := range cases {
		p := NewPlan("", nil)
		p.Add(&Node{ID: "n", Terminal: true, Body: tc.body})
		sch := NewScheduler(SchedulerOptions{})
		run, err := sch.Run(p, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if run.State != tc.state {
			t.Fatalf("state = %q, want %q", run.State, tc.state)
		}
		kinds := eventKinds(t, sch, run.RunID)
		if kinds[len(kinds)-1] != tc.kind {
			t.Fatalf("last kind = %q, want %q", kinds[len(kinds)-1], tc.kind)
		}
	}
}
