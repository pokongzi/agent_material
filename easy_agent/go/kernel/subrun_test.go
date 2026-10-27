package kernel

// Multi-agent：子 Agent 是由某个 Body 递归驱动的子 Run，用的是同一套内核。
// （test_subrun.py 内核相关用例的 Go 对应版本。）

import (
	"strings"
	"sync"
	"testing"
)

func subrunChildPlan() *Plan {
	c := NewPlan("", map[string]Channel{"trace": Append(nil)})
	c.Add(&Node{ID: "work", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
		return OKDelta("child-result", map[string]any{"trace": []any{x}}), nil
	})})
	return c
}

// observeRunStarted 在 bus 上收集每个 run_started 事实的 parent_id（根 Run 自身为 ""）。
func observeRunStarted(sch *Scheduler) (*[]string, *sync.Mutex) {
	var mu sync.Mutex
	ids := &[]string{}
	sch.Bus().On(EvRunStarted, func(data map[string]any) {
		ev, _ := data["evt"].(Event)
		mu.Lock()
		*ids = append(*ids, ev.ParentID)
		mu.Unlock()
	})
	return ids, &mu
}

func TestSubrunCallReturnsOutputToParent(t *testing.T) {
	parent := NewPlan("", nil)
	parent.Add(&Node{ID: "delegate", Terminal: true, Body: &SubPlanBody{Spec: subrunChildPlan()}})
	sch := NewScheduler(SchedulerOptions{})
	ids, mu := observeRunStarted(sch)

	run, err := sch.Run(parent, "给子Agent的任务", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunCompleted {
		t.Fatalf("state = %q, want completed", run.State)
	}
	// Call 语义：只有子 Run 的最终输出被交还父级。
	if run.FinalOutput != "child-result" {
		t.Fatalf("final = %v, want child-result", run.FinalOutput)
	}
	// 子 Run 的 run_started 事件带 parent_id，Run 树可据此重建。
	mu.Lock()
	childStarts := 0
	for _, pid := range *ids {
		if pid == run.RunID {
			childStarts++
		}
	}
	mu.Unlock()
	if childStarts != 1 {
		t.Fatalf("child run_started facts = %d, want 1", childStarts)
	}
}

func TestNestedSubrunsBuildARunTree(t *testing.T) {
	// grandparent -> parent（Body 是子 Run）-> child；每层只有输出向上传递。
	grandchild := subrunChildPlan()
	middle := NewPlan("", nil)
	middle.Add(&Node{ID: "m", Terminal: true, Body: &SubPlanBody{Spec: grandchild}})
	root := NewPlan("", nil)
	root.Add(&Node{ID: "r", Terminal: true, Body: &SubPlanBody{Spec: middle}})

	run, err := NewScheduler(SchedulerOptions{}).Run(root, "deep", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunCompleted {
		t.Fatalf("state = %q, want completed", run.State)
	}
	if run.FinalOutput != "child-result" {
		t.Fatalf("final = %v, want child-result", run.FinalOutput)
	}
}

func TestMutualDelegationLoopIsCapped(t *testing.T) {
	// A 派生 B、B 又派生 A：没有深度熔断会无限递归，所以必须在 Run 树深度上限处失败（出生时强制）。
	planA := NewPlan("", nil)
	planB := NewPlan("", nil)
	planA.Add(&Node{ID: "a", Terminal: true, Body: &SubPlanBody{Spec: planB}})
	planB.Add(&Node{ID: "b", Terminal: true, Body: &SubPlanBody{Spec: planA}})

	run, err := NewScheduler(SchedulerOptions{}).Run(planA, "mutual delegation", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunFailed {
		t.Fatalf("state = %q, want failed", run.State)
	}
	if s, _ := run.FinalOutput.(string); !strings.Contains(s, "depth") {
		t.Fatalf("final = %q, want it to mention depth", s)
	}
}

func TestRunNameIsBlueprintIdentityNotState(t *testing.T) {
	child := NewPlan("child-expert", map[string]Channel{"trace": Append(nil)})
	child.Add(&Node{ID: "work", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
		return OKDelta("child-result", map[string]any{"trace": []any{x}}), nil
	})})
	parent := NewPlan("", nil)
	parent.Add(&Node{ID: "delegate", Terminal: true, Body: &SubPlanBody{Spec: child}})

	sch := NewScheduler(SchedulerOptions{})
	var mu sync.Mutex
	var names []string
	sch.Bus().On(EvRunStarted, func(data map[string]any) {
		ev, _ := data["evt"].(Event)
		name, _ := ev.Data["name"].(string)
		mu.Lock()
		names = append(names, name)
		mu.Unlock()
	})

	run, err := sch.Run(parent, "给子Agent的任务", nil)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	hasChild, hasAnon := false, false
	for _, n := range names {
		if n == "child-expert" {
			hasChild = true
		}
		if n == "" {
			hasAnon = true
		}
	}
	mu.Unlock()
	if !hasChild || !hasAnon {
		t.Fatalf("names = %v, want both child-expert and the anonymous parent", names)
	}
	if run.Name() != "" {
		t.Fatalf("parent name = %q, want empty", run.Name())
	}
	// 静态身份不是 Run 状态：同一事件流的重放始终从手头蓝图推导它。
	events, _ := sch.EventLog().Events(run.RunID)
	r2, err := Replay(parent, events)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Name() != "" {
		t.Fatalf("replayed name = %q, want empty", r2.Name())
	}
}
