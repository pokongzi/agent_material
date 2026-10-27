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

func TestAFailedWaveStillSettlesItsSiblings(t *testing.T) {
	// fail-fast 只停 Run 不停结算：成功的兄弟 Node 保留折叠结果与 NODE_COMPLETED，失败节点记 NODE_FAILED。
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

func TestFailureWinsOverASameWaveParkButTheAskIsLogged(t *testing.T) {
	// 同一波中一个 Node park、兄弟 Node 失败时 Run 判失败，但 park 的提问仍是事件流必须承载的事实。
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

func TestAGotoPayloadSurvivesAPark(t *testing.T) {
	// Goto 携带的输入由 Node 的终态消费，而非靠读取消费：park 的 Node 在 resume 后以同一输入重跑。
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

func TestTerminalStatesCloseTheStream(t *testing.T) {
	// 已结束 Run 的 Outcome 是日志里的事实——唯一持久之物。
	// 结束后不会有任何东西还停留在 RUNNING。
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
