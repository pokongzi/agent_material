package kernel

// Interrupt：Node 请求放手挂起，事实落入事件日志；resume 重放事件流、回灌外部值，
// 重跑被 park 的 Node 并继续执行。（test_interrupt.py 的 Go 对应版本。）

import (
	"reflect"
	"testing"
)

// buildApprovalPlan 首次执行时 park，resume 时回显传入值。
func buildApprovalPlan() *Plan {
	p := NewPlan("", nil)
	p.Add(&Node{ID: "approve", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
		if ctx.ResumeValue == nil {
			return Park("approval", nil, "批准吗？"), nil
		}
		return OK(ctx.ResumeValue), nil
	})})
	return p
}

func askBody(name string) Fn {
	return func(x any, ctx *NodeContext) (any, error) {
		if ctx.ResumeValue == nil {
			return Park("approval", nil, name+" 批准吗？"), nil
		}
		return OK(ctx.ResumeValue), nil
	}
}

func buildTwoApprovalsPlan() *Plan {
	p := NewPlan("", nil)
	p.Add(
		&Node{ID: "a", Terminal: true, Body: NewFnBody(askBody("a"))},
		&Node{ID: "b", Terminal: true, Body: NewFnBody(askBody("b"))},
	)
	return p
}

func TestSuspendThenResume(t *testing.T) {
	sch := NewScheduler(SchedulerOptions{})
	run, err := sch.Run(buildApprovalPlan(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunSuspended {
		t.Fatalf("state = %q, want suspended", run.State)
	}
	if run.Interrupts["approve"] == nil || run.Interrupts["approve"].Question != "批准吗？" {
		t.Fatalf("interrupt = %+v", run.Interrupts["approve"])
	}

	run2, err := sch.Resume(buildApprovalPlan(), run.RunID, map[string]any{"approved": true}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if run2.State != RunCompleted {
		t.Fatalf("resumed state = %q, want completed", run2.State)
	}
	if !reflect.DeepEqual(run2.FinalOutput, map[string]any{"approved": true}) {
		t.Fatalf("final = %v, want map[approved:true]", run2.FinalOutput)
	}
}

func TestResumeRejectsADriftedPlan(t *testing.T) {
	// 事件流归属其蓝图：拿静态 Node 集不同的 Plan 去 resume 必须大声失败，而非静默错接。
	sch := NewScheduler(SchedulerOptions{})
	run, err := sch.Run(buildApprovalPlan(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunSuspended {
		t.Fatalf("state = %q, want suspended", run.State)
	}

	drifted := NewPlan("", nil)
	drifted.Add(&Node{ID: "elsewhere", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
		return x, nil
	})})
	if _, err := sch.Resume(drifted, run.RunID, "ok", nil, nil); err == nil {
		t.Fatal("resuming against a drifted plan must fail loudly")
	}
}

func TestTwoNodesParkInTheSameWave(t *testing.T) {
	sch := NewScheduler(SchedulerOptions{})
	run, err := sch.Run(buildTwoApprovalsPlan(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunSuspended {
		t.Fatalf("state = %q, want suspended", run.State)
	}
	if len(run.Interrupts) != 2 || run.Interrupts["a"] == nil || run.Interrupts["b"] == nil {
		t.Fatalf("interrupts = %v, want both a and b", run.Interrupts)
	}

	run2, err := sch.Resume(buildTwoApprovalsPlan(), run.RunID, map[string]any{"a": "yes-a", "b": "yes-b"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if run2.State != RunCompleted {
		t.Fatalf("resumed state = %q, want completed", run2.State)
	}
	if !reflect.DeepEqual(run2.FinalOutput, map[string]any{"a": "yes-a", "b": "yes-b"}) {
		t.Fatalf("final = %v", run2.FinalOutput)
	}
}

func TestResumeWithAFreshSchedulerSharedLog(t *testing.T) {
	// 换一个全新 Scheduler 接同一事件日志仍能继续（模拟进程重启）：resume 是已记录事实的纯函数。
	log := NewInMemoryEventLog()
	sch1 := NewScheduler(SchedulerOptions{EventLog: log})
	run, err := sch1.Run(buildApprovalPlan(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunSuspended {
		t.Fatalf("state = %q, want suspended", run.State)
	}

	sch2 := NewScheduler(SchedulerOptions{EventLog: log})
	run2, err := sch2.Resume(buildApprovalPlan(), run.RunID, "ok", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if run2.State != RunCompleted {
		t.Fatalf("resumed state = %q, want completed", run2.State)
	}
	if run2.FinalOutput != "ok" {
		t.Fatalf("final = %v, want ok", run2.FinalOutput)
	}
}

func TestResumeAfterAMidWaveCrash(t *testing.T) {
	// Node 在途时崩溃的 Run 重放为 RUNNING；resume 重新武装并再次驱动该 Node——至少一次，
	// 所以 park-first 的 Node 会重新提问，而不是收到从未记录的答案。
	log := NewInMemoryEventLog()
	sch := NewScheduler(SchedulerOptions{EventLog: log})
	run, err := sch.Run(buildApprovalPlan(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunSuspended {
		t.Fatalf("state = %q, want suspended", run.State)
	}

	// 模拟崩溃截断：interruption 从未落盘——进程死亡时 Node 仍在途。
	events, _ := log.Events(run.RunID)
	var kept []Event
	for _, e := range events {
		if e.Kind != EvInterrupted {
			kept = append(kept, e)
		}
	}
	log.streams[run.RunID] = kept

	run2, err := sch.Resume(buildApprovalPlan(), run.RunID, nil, nil, nil) // 没有被 park 的节点：无值可回灌
	if err != nil {
		t.Fatal(err)
	}
	if run2.State != RunSuspended {
		t.Fatalf("crash-continue state = %q, want suspended (the node honestly re-parked)", run2.State)
	}

	run3, err := sch.Resume(buildApprovalPlan(), run2.RunID, map[string]any{"approved": true}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if run3.State != RunCompleted {
		t.Fatalf("final state = %q, want completed", run3.State)
	}
	if !reflect.DeepEqual(run3.FinalOutput, map[string]any{"approved": true}) {
		t.Fatalf("final = %v", run3.FinalOutput)
	}
}

func TestFinishedRunRefusesResume(t *testing.T) {
	// 已完成的历史不可 resume——事实已关闭。
	sch := NewScheduler(SchedulerOptions{})
	donePlan := NewPlan("", nil)
	donePlan.Add(&Node{ID: "done", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
		return OK("x"), nil
	})})
	run, err := sch.Run(donePlan, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunCompleted {
		t.Fatalf("state = %q, want completed", run.State)
	}
	if _, err := sch.Resume(donePlan, run.RunID, "again", nil, nil); err == nil {
		t.Fatal("a finished run must refuse to resume")
	}
}
