package kernel

// Replay：仅凭事件流，用纯投影折叠（无 scheduler、无 Body、无 LLM、无工具），
// 就能重建出与实时执行等价的 Run。（test_replay.py 内核相关用例的 Go 对应版本。）

import (
	"fmt"
	"reflect"
	"testing"
)

func TestReplayRebuildsDynamicFanout(t *testing.T) {
	// plan-first 形态：planner 向模板 worker Send N 份任务再 re-join synth；
	// 重放须还原各实例、其输出、共享状态与最终结局。
	planner := func(x any, ctx *NodeContext) (any, error) {
		steps := []any{
			map[string]any{"id": "s1"},
			map[string]any{"id": "s2"},
			map[string]any{"id": "s3"},
		}
		return Outcome{
			StateDelta: map[string]any{"steps": steps},
			Control: []Command{
				Send{Template: "worker", Payload: steps[0], Key: "s1"},
				Send{Template: "worker", Payload: steps[1], Key: "s2"},
				Send{Template: "worker", Payload: steps[2], Key: "s3"},
				GotoRejoin("synth", nil),
			},
		}, nil
	}
	worker := func(x any, ctx *NodeContext) (any, error) {
		m, _ := x.(map[string]any)
		id, _ := m["id"].(string)
		return OK("done-" + id), nil
	}
	synth := func(x any, ctx *NodeContext) (any, error) { return OK(x), nil }

	p := NewPlan("", map[string]Channel{"steps": Last([]any{})})
	p.Add(
		&Node{ID: "planner", Body: NewFnBody(planner)},
		&Node{ID: "worker", Template: true, Body: NewFnBody(worker)},
		&Node{ID: "synth", Terminal: true, Body: NewFnBody(synth)},
	)
	p.Edge("worker", "synth", nil)
	p.Entry = []string{"planner"}

	sch := NewScheduler(SchedulerOptions{})
	run, err := sch.Run(p, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunCompleted {
		t.Fatalf("live state = %q, want completed", run.State)
	}

	events, err := sch.EventLog().Events(run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := Replay(p, events)
	if err != nil {
		t.Fatal(err)
	}
	if r2.State != RunCompleted {
		t.Fatalf("replayed state = %q, want completed", r2.State)
	}
	if !reflect.DeepEqual(r2.Shared["steps"], run.Shared["steps"]) {
		t.Fatalf("steps = %v, want %v", r2.Shared["steps"], run.Shared["steps"])
	}
	if !reflect.DeepEqual(sortedStrs(r2.Instances["worker"]), sortedStrs(run.Instances["worker"])) {
		t.Fatalf("instances = %v, want %v", r2.Instances["worker"], run.Instances["worker"])
	}
	for _, key := range run.Instances["worker"] {
		if r2.StateOf(key).Status != NodeCompleted {
			t.Fatalf("%s replayed status = %q, want completed", key, r2.StateOf(key).Status)
		}
		if !reflect.DeepEqual(r2.StateOf(key).Output, run.StateOf(key).Output) {
			t.Fatalf("%s output = %v, want %v", key, r2.StateOf(key).Output, run.StateOf(key).Output)
		}
	}
	if r2.StateOf("synth").Status != NodeCompleted {
		t.Fatalf("synth = %q, want completed", r2.StateOf("synth").Status)
	}
	if !reflect.DeepEqual(sortedStrings(r2.FinalOutput), sortedStrings(run.FinalOutput)) {
		t.Fatalf("final = %v, want %v", r2.FinalOutput, run.FinalOutput)
	}
	if len(r2.Deliveries) != 0 {
		t.Fatalf("deliveries = %v, want empty", r2.Deliveries)
	}
}

func TestReplayRebuildsGotoBackEdge(t *testing.T) {
	// 立即 Goto 每波重新武装同一 Node；重放须走完循环、落到终态 Node，且折叠状态一致。
	loop := func(x any, ctx *NodeContext) (any, error) {
		n := toInt(ctx.Shared()["n"]) + 1
		target := "done"
		if n < 3 {
			target = "loop"
		}
		return GotoOutcome(target, nil, map[string]any{"n": n}), nil
	}
	p := NewPlan("", map[string]Channel{"n": Last(0)})
	p.Add(
		&Node{ID: "loop", Body: NewFnBody(loop)},
		&Node{ID: "done", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			return OK(x), nil
		})},
	)
	p.Edge("loop", "done", func(s map[string]any) bool { return toInt(s["n"]) >= 3 })
	p.Entry = []string{"loop"}

	sch := NewScheduler(SchedulerOptions{})
	run, err := sch.Run(p, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	events, _ := sch.EventLog().Events(run.RunID)
	r2, err := Replay(p, events)
	if err != nil {
		t.Fatal(err)
	}
	if r2.State != RunCompleted {
		t.Fatalf("replayed state = %q, want completed", r2.State)
	}
	if toInt(r2.Shared["n"]) != 3 {
		t.Fatalf("n = %v, want 3", r2.Shared["n"])
	}
	if r2.StateOf("loop").Status != NodeCompleted {
		t.Fatalf("loop = %q, want completed", r2.StateOf("loop").Status)
	}
	if !reflect.DeepEqual(r2.FinalOutput, run.FinalOutput) {
		t.Fatalf("final = %v, want %v", r2.FinalOutput, run.FinalOutput)
	}
}

func TestReplayMarksUntakenBranchSkipped(t *testing.T) {
	// 条件边求值为 false 会结构性跳过分支；重放依 node_skipped 事件同样将其标为 skipped。
	p := NewPlan("", map[string]Channel{"route": Last(nil)})
	p.Add(
		&Node{ID: "decide", Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			return OKDelta(nil, map[string]any{"route": "a"}), nil
		})},
		&Node{ID: "a", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) { return OK("A"), nil })},
		&Node{ID: "b", Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) { return OK("B"), nil })},
	)
	p.Edge("decide", "a", func(s map[string]any) bool { r, _ := s["route"].(string); return r == "a" })
	p.Edge("decide", "b", func(s map[string]any) bool { r, _ := s["route"].(string); return r == "b" })

	sch := NewScheduler(SchedulerOptions{})
	run, err := sch.Run(p, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunCompleted {
		t.Fatalf("live state = %q, want completed", run.State)
	}
	events, _ := sch.EventLog().Events(run.RunID)
	r2, err := Replay(p, events)
	if err != nil {
		t.Fatal(err)
	}
	if r2.StateOf("a").Status != NodeCompleted {
		t.Fatalf("a = %q, want completed", r2.StateOf("a").Status)
	}
	if r2.StateOf("b").Status != NodeSkipped {
		t.Fatalf("b = %q, want skipped", r2.StateOf("b").Status)
	}
	if r2.FinalOutput != "A" {
		t.Fatalf("final = %v, want A", r2.FinalOutput)
	}
}

func TestReplayRebuildsASuspension(t *testing.T) {
	// Run park 在人工确认上：重放须停在 SUSPENDED，且问题与 payload 一致，无人真正作答。
	p := NewPlan("", nil)
	p.Add(
		&Node{ID: "ask", Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			return Park("input", map[string]any{"order": "o1"}, "need the order id"), nil
		})},
		&Node{ID: "after", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			return OK("finished"), nil
		})},
	)
	p.Edge("ask", "after", nil)

	sch := NewScheduler(SchedulerOptions{})
	run, err := sch.Run(p, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunSuspended {
		t.Fatalf("live state = %q, want suspended", run.State)
	}
	events, _ := sch.EventLog().Events(run.RunID)
	r2, err := Replay(p, events)
	if err != nil {
		t.Fatal(err)
	}
	if r2.State != RunSuspended {
		t.Fatalf("replayed state = %q, want suspended", r2.State)
	}
	parked := r2.Interrupts["ask"]
	if parked == nil {
		t.Fatal("ask should be parked")
	}
	if parked.Question != "need the order id" {
		t.Fatalf("question = %q", parked.Question)
	}
	if !reflect.DeepEqual(parked.Payload, map[string]any{"order": "o1"}) {
		t.Fatalf("payload = %v", parked.Payload)
	}
}

func TestReplayMarksEveryNodeOfAFailedWave(t *testing.T) {
	// 失败会结算整个波：每个失败 Node 依各自的 node_failed 事件标记，而非只标 run_failed 点名的那个。
	boom := func(x any, ctx *NodeContext) (any, error) { return nil, fmt.Errorf("boom") }
	p := NewPlan("", nil)
	p.Add(
		&Node{ID: "f1", Terminal: true, Body: NewFnBody(boom)},
		&Node{ID: "f2", Terminal: true, Body: NewFnBody(boom)},
	)
	sch := NewScheduler(SchedulerOptions{})
	run, err := sch.Run(p, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	events, _ := sch.EventLog().Events(run.RunID)
	r2, err := Replay(p, events)
	if err != nil {
		t.Fatal(err)
	}
	if r2.State != RunFailed {
		t.Fatalf("replayed state = %q, want failed", r2.State)
	}
	if r2.StateOf("f1").Status != NodeFailed {
		t.Fatalf("f1 = %q, want failed", r2.StateOf("f1").Status)
	}
	if r2.StateOf("f2").Status != NodeFailed {
		t.Fatalf("f2 = %q, want failed", r2.StateOf("f2").Status)
	}
}

func TestReplayKeepsAParkedNodesGotoInput(t *testing.T) {
	// 被 park 的 Node 的 Goto input 要留存到重跑；仅凭事件流也须重建它。
	p := NewPlan("", nil)
	p.Add(
		&Node{ID: "start", Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			return GotoOutcome("worker", "brief", nil), nil
		})},
		&Node{ID: "worker", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			if ctx.ResumeValue == nil {
				return Park("input", nil, "more?"), nil
			}
			return OK("kept"), nil
		})},
	)
	p.Edge("start", "worker", nil)

	sch := NewScheduler(SchedulerOptions{})
	run, err := sch.Run(p, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	events, _ := sch.EventLog().Events(run.RunID)
	r2, err := Replay(p, events)
	if err != nil {
		t.Fatal(err)
	}
	if r2.State != RunSuspended {
		t.Fatalf("replayed state = %q, want suspended", r2.State)
	}
	want := map[string]any{"worker": "brief"}
	if !reflect.DeepEqual(run.Deliveries, want) {
		t.Fatalf("live deliveries = %v, want %v", run.Deliveries, want)
	}
	if !reflect.DeepEqual(r2.Deliveries, want) {
		t.Fatalf("replayed deliveries = %v, want %v", r2.Deliveries, want)
	}
}

func TestReplayRequiresARunStartedEvent(t *testing.T) {
	p := NewPlan("", nil)
	p.Add(&Node{ID: "a", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
		return "x", nil
	})})
	if _, err := Replay(p, nil); err == nil {
		t.Fatal("replaying an empty stream must fail loudly")
	}
}
