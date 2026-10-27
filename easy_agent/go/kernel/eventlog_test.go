package kernel

// 事件溯源：状态是由事件流折叠出的投影，Run 的两种结束方式都会落入日志。
// （test_eventlog.py 的 Go 对应版本。）

import (
	"reflect"
	"testing"
)

func TestStateEqualsFoldOfEventStream(t *testing.T) {
	p := NewPlan("", map[string]Channel{"log": Append(nil), "n": Add(0)})
	p.Add(
		&Node{ID: "a", Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			return OKDelta(nil, map[string]any{"log": []any{"a"}, "n": 1}), nil
		})},
		&Node{ID: "b", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			return OKDelta(nil, map[string]any{"log": []any{"b"}, "n": 2}), nil
		})},
	)
	p.Edge("a", "b", nil)
	sch := NewScheduler(SchedulerOptions{})
	run, err := sch.Run(p, "", nil)
	if err != nil {
		t.Fatal(err)
	}

	events, err := sch.EventLog().Events(run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var deltas []Event
	for _, e := range events {
		if e.Kind == EvStateDelta {
			deltas = append(deltas, e)
		}
	}
	if len(deltas) == 0 {
		t.Fatal("state-delta events should have been recorded")
	}

	// 从初始状态重放整个 delta 流，必须精确还原 Run 的共享状态。
	rebuilt := FoldEvents(deltas, p.Channels, p.InitialShared())
	if !reflect.DeepEqual(rebuilt, run.Shared) {
		t.Fatalf("rebuilt = %v, want %v", rebuilt, run.Shared)
	}
}

func TestRunEndIsRecordedInTheEventStream(t *testing.T) {
	// 正常完成的 Run 以 run_completed 收尾。
	p := NewPlan("", nil)
	p.Add(&Node{ID: "a", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
		return "done", nil
	})})
	sch := NewScheduler(SchedulerOptions{})
	run, err := sch.Run(p, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	kinds := eventKinds(t, sch, run.RunID)
	if kinds[len(kinds)-1] != EvRunCompleted {
		t.Fatalf("last kind = %q, want %q", kinds[len(kinds)-1], EvRunCompleted)
	}

	// 无入口的环会停滞，以 run_failed 收尾。
	cyc := NewPlan("", nil)
	cyc.Add(
		&Node{ID: "a", Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) { return OK("A"), nil })},
		&Node{ID: "b", Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) { return OK("B"), nil })},
	)
	cyc.Edge("a", "b", nil)
	cyc.Edge("b", "a", nil)
	stalled, err := sch.Run(cyc, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if stalled.State != RunFailed {
		t.Fatalf("state = %q, want %q", stalled.State, RunFailed)
	}
	kinds = eventKinds(t, sch, stalled.RunID)
	if kinds[len(kinds)-1] != EvRunFailed {
		t.Fatalf("last kind = %q, want %q", kinds[len(kinds)-1], EvRunFailed)
	}
}

func eventKinds(t *testing.T, sch *Scheduler, runID string) []string {
	t.Helper()
	events, err := sch.EventLog().Events(runID)
	if err != nil {
		t.Fatal(err)
	}
	kinds := make([]string, len(events))
	for i, e := range events {
		kinds[i] = e.Kind
	}
	return kinds
}
