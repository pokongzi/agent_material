package kernel

// Trace 投影：事件流折叠成 Run 的因果树。（test_trace.py 内核相关用例的 Go 对应版本。）

import (
	"strings"
	"testing"
)

func traceChildPlan() *Plan {
	plan := NewPlan("worker", nil)
	plan.Add(&Node{ID: "work", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
		return "child done", nil
	})})
	plan.Entry = []string{"work"}
	return plan
}

func traceParentPlan(child *Plan) *Plan {
	plan := NewPlan("boss", nil)
	plan.Add(&Node{ID: "delegate", Terminal: true, Body: &SubPlanBody{Spec: child}})
	plan.Entry = []string{"delegate"}
	return plan
}

func TestTraceTreeLinksParentAndChild(t *testing.T) {
	sch := NewScheduler(SchedulerOptions{})
	run, err := sch.Run(traceParentPlan(traceChildPlan()), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	all, err := sch.EventLog().AllEvents()
	if err != nil {
		t.Fatal(err)
	}
	roots := BuildTrace(all)
	if len(roots) != 1 {
		t.Fatalf("roots = %d, want 1", len(roots))
	}
	root := roots[0]
	if root.RunID != run.RunID {
		t.Fatalf("root run = %q, want %q", root.RunID, run.RunID)
	}
	if root.Name != "boss" {
		t.Fatalf("root name = %q, want boss", root.Name)
	}
	if len(root.Children) != 1 {
		t.Fatalf("children = %d, want 1", len(root.Children))
	}
	if root.Children[0].Name != "worker" {
		t.Fatalf("child name = %q, want worker", root.Children[0].Name)
	}
}

func TestRenderTraceIndentsChildUnderParent(t *testing.T) {
	sch := NewScheduler(SchedulerOptions{})
	if _, err := sch.Run(traceParentPlan(traceChildPlan()), "", nil); err != nil {
		t.Fatal(err)
	}
	all, _ := sch.EventLog().AllEvents()
	text := RenderTrace(all)
	if !strings.Contains(text, "● boss") {
		t.Fatalf("missing boss header in:\n%s", text)
	}
	if !strings.Contains(text, "● worker") {
		t.Fatalf("missing worker header in:\n%s", text)
	}
	// 子 Run 的标题缩进比父级更深（因此排在其后）。
	if strings.Index(text, "● worker") < strings.Index(text, "● boss") {
		t.Fatalf("worker not under boss in:\n%s", text)
	}
	if !strings.Contains(text, EvNodeCompleted) {
		t.Fatalf("facts of the run are not shown in:\n%s", text)
	}
}

func TestSingleRunTraceHasNoChildren(t *testing.T) {
	plan := NewPlan("solo", nil)
	plan.Add(&Node{ID: "step", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
		return 42, nil
	})})
	plan.Entry = []string{"step"}
	sch := NewScheduler(SchedulerOptions{})
	if _, err := sch.Run(plan, "", nil); err != nil {
		t.Fatal(err)
	}
	all, _ := sch.EventLog().AllEvents()
	roots := BuildTrace(all)
	if len(roots) != 1 {
		t.Fatalf("roots = %d, want 1", len(roots))
	}
	if len(roots[0].Children) != 0 {
		t.Fatalf("children = %d, want 0", len(roots[0].Children))
	}
}

func TestTraceDurationsDeriveFromEventTimestamps(t *testing.T) {
	// Trace 用 ts 相减得出时长；该字段必须保留，否则从持久化日志渲染的 Trace 会静默显示为 0。
	events := []Event{
		{Seq: 1, RunID: "r1", Kind: EvRunStarted, Data: map[string]any{"name": "a", "task": "t"}, Ts: 10.0},
		{Seq: 2, RunID: "r1", Kind: EvNodeStarted, Data: map[string]any{"node": "x"}, Ts: 10.5},
		{Seq: 3, RunID: "r1", Kind: EvRunCompleted, Data: map[string]any{}, Ts: 12.5},
	}
	roots := BuildTrace(events)
	if len(roots) != 1 {
		t.Fatalf("roots = %d, want 1", len(roots))
	}
	if roots[0].DurationMS() != 2500 {
		t.Fatalf("duration = %dms, want 2500", roots[0].DurationMS())
	}
}
