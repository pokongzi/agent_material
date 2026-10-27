package kernel

// 图与调度：顺序执行、菱形并发、条件分支、回边循环、动态 fan-out/fan-in、
// 停滞与自旋、join 策略，以及 L1/L2 Body 梯级。
// （test_graph_scheduler.py + test_call_ids.py 的 Go 对应版本。）

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestLinear(t *testing.T) {
	p := NewPlan("", map[string]Channel{"log": Append(nil)})
	p.Add(
		&Node{ID: "a", Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			return OKDelta("A", map[string]any{"log": []any{"a"}}), nil
		})},
		&Node{ID: "b", Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			s, _ := x.(string)
			return OKDelta(s + "B", map[string]any{"log": []any{"b"}}), nil
		})},
		&Node{ID: "c", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			s, _ := x.(string)
			return OK(s + "C"), nil
		})},
	)
	p.Edge("a", "b", nil)
	p.Edge("b", "c", nil)
	run, err := NewScheduler(SchedulerOptions{}).Run(p, "t", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunCompleted {
		t.Fatalf("state = %q, want completed", run.State)
	}
	if run.FinalOutput != "ABC" {
		t.Fatalf("final = %v, want ABC", run.FinalOutput)
	}
	if !reflect.DeepEqual(run.Shared["log"], []any{"a", "b"}) {
		t.Fatalf("log = %v, want [a b]", run.Shared["log"])
	}
	if run.Metrics["waves"] != 3 {
		t.Fatalf("waves = %d, want 3", run.Metrics["waves"])
	}
}

func TestDiamondParallelReducers(t *testing.T) {
	p := NewPlan("", map[string]Channel{"items": Append(nil), "n": Add(0)})
	p.Add(
		&Node{ID: "a", Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			return OKDelta(nil, map[string]any{"items": []any{"a"}, "n": 1}), nil
		})},
		&Node{ID: "b", Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			return OKDelta("B", map[string]any{"items": []any{"b"}, "n": 1}), nil
		})},
		&Node{ID: "c", Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			return OKDelta("C", map[string]any{"items": []any{"c"}, "n": 1}), nil
		})},
		&Node{ID: "d", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			return OK(sortedStrings(ctx.Shared()["items"])), nil
		})},
	)
	p.Edge("a", "b", nil)
	p.Edge("a", "c", nil)
	p.Edge("b", "d", nil)
	p.Edge("c", "d", nil)
	run, err := NewScheduler(SchedulerOptions{}).Run(p, "t", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunCompleted {
		t.Fatalf("state = %q, want completed", run.State)
	}
	if !reflect.DeepEqual(run.FinalOutput, []any{"a", "b", "c"}) {
		t.Fatalf("final = %v, want [a b c]", run.FinalOutput)
	}
	if toInt(run.Shared["n"]) != 3 {
		t.Fatalf("n = %v, want 3", run.Shared["n"])
	}
}

func TestConditionalBranchSkipsDeadPath(t *testing.T) {
	p := NewPlan("", map[string]Channel{"go_left": Last(false)})
	p.Add(
		&Node{ID: "start", Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			return OKDelta(nil, map[string]any{"go_left": true}), nil
		})},
		&Node{ID: "left", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			return OK("L"), nil
		})},
		&Node{ID: "right", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			return OK("R"), nil
		})},
	)
	p.Edge("start", "left", func(s map[string]any) bool { b, _ := s["go_left"].(bool); return b })
	p.Edge("start", "right", func(s map[string]any) bool { b, _ := s["go_left"].(bool); return !b })
	run, err := NewScheduler(SchedulerOptions{}).Run(p, "t", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.FinalOutput != "L" {
		t.Fatalf("final = %v, want L", run.FinalOutput)
	}
	if run.StateOf("right").Status != NodeSkipped {
		t.Fatalf("right = %q, want skipped", run.StateOf("right").Status)
	}
}

func TestGotoLoop(t *testing.T) {
	p := NewPlan("", map[string]Channel{"c": Add(0)})
	p.Add(&Node{ID: "tick", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
		if toInt(ctx.Shared()["c"]) < 2 {
			return GotoOutcome("tick", nil, map[string]any{"c": 1}), nil
		}
		return OKDelta("stop", map[string]any{"c": 1}), nil
	})})
	run, err := NewScheduler(SchedulerOptions{}).Run(p, "t", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.FinalOutput != "stop" {
		t.Fatalf("final = %v, want stop", run.FinalOutput)
	}
	if toInt(run.Shared["c"]) != 3 {
		t.Fatalf("c = %v, want 3", run.Shared["c"])
	}
}

func TestDynamicFanoutFanin(t *testing.T) {
	p := NewPlan("", map[string]Channel{"outs": Append(nil)})
	p.Add(
		&Node{ID: "fan", Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			// 裸 Send 列表自动收编为控制组——无需专门词汇即可 fan-out。
			return []Command{
				Send{Template: "worker", Payload: 1, Key: "w1"},
				Send{Template: "worker", Payload: 2, Key: "w2"},
			}, nil
		})},
		&Node{ID: "worker", Template: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			return OKDelta(nil, map[string]any{"outs": []any{toInt(x) * 10}}), nil
		})},
		&Node{ID: "join", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			return OK(sortedIntsAny(ctx.Shared()["outs"])), nil
		})},
	)
	p.Edge("worker", "join", nil)
	run, err := NewScheduler(SchedulerOptions{}).Run(p, "t", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(run.FinalOutput, []any{10, 20}) {
		t.Fatalf("final = %v, want [10 20]", run.FinalOutput)
	}
	if !reflect.DeepEqual(sortedStrs(run.Instances["worker"]), []string{"worker#w1", "worker#w2"}) {
		t.Fatalf("instances = %v", run.Instances["worker"])
	}
}

func TestStagnationFails(t *testing.T) {
	p := NewPlan("", nil)
	p.Add(
		&Node{ID: "a", Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) { return OK("A"), nil })},
		&Node{ID: "b", Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) { return OK("B"), nil })},
	)
	p.Edge("a", "b", nil)
	p.Edge("b", "a", nil) // a 等 b，b 等 a，且没有入口 Node
	run, err := NewScheduler(SchedulerOptions{}).Run(p, "t", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunFailed {
		t.Fatalf("state = %q, want failed", run.State)
	}
	if s, _ := run.FinalOutput.(string); !strings.Contains(s, "stalled") {
		t.Fatalf("final = %q, want it to mention stalled", s)
	}
}

func TestMaxWavesGuardsSpin(t *testing.T) {
	p := NewPlan("", map[string]Channel{"c": Add(0)})
	p.Add(&Node{ID: "spin", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
		return GotoOutcome("spin", nil, map[string]any{"c": 1}), nil
	})})
	run, err := NewScheduler(SchedulerOptions{MaxWaves: 5}).Run(p, "t", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunFailed {
		t.Fatalf("state = %q, want failed", run.State)
	}
	if s, _ := run.FinalOutput.(string); !strings.Contains(s, "max waves") {
		t.Fatalf("final = %q, want it to mention max waves", s)
	}
}

func TestIllegalTransitionRejected(t *testing.T) {
	p := NewPlan("", nil)
	p.Add(&Node{ID: "a", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
		return OK("A"), nil
	})})
	run, err := NewScheduler(SchedulerOptions{}).Run(p, "t", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := run.Complete("again"); err == nil {
		t.Fatal("an already-completed run cannot complete again")
	}
}

func TestInvalidJoinRejectedAtValidate(t *testing.T) {
	p := NewPlan("", nil)
	p.Add(&Node{ID: "a", Join: JoinSpec{mode: "most"}, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
		return OK("A"), nil
	})})
	err := p.Validate()
	if err == nil || !strings.Contains(err.Error(), "invalid join") {
		t.Fatalf("validate err = %v, want it to mention invalid join", err)
	}
}

func TestJoinAnyFiresWhenASiblingWasStructurallySkipped(t *testing.T) {
	p := NewPlan("", map[string]Channel{"go_a": Last(true)})
	p.Add(
		&Node{ID: "start", Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			return OKDelta(nil, map[string]any{"go_a": true}), nil
		})},
		&Node{ID: "a", Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) { return OK("A"), nil })},
		&Node{ID: "b", Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) { return OK("B"), nil })},
		&Node{ID: "c", Join: JoinAny(), Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			return OK("C"), nil
		})},
	)
	p.Edge("start", "a", func(s map[string]any) bool { b, _ := s["go_a"].(bool); return b })
	p.Edge("start", "b", func(s map[string]any) bool { b, _ := s["go_a"].(bool); return !b })
	p.Edge("a", "c", nil)
	p.Edge("b", "c", nil)
	run, err := NewScheduler(SchedulerOptions{}).Run(p, "t", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunCompleted {
		t.Fatalf("state = %q, want completed", run.State)
	}
	if run.FinalOutput != "C" {
		t.Fatalf("final = %v, want C", run.FinalOutput)
	}
	if run.StateOf("b").Status != NodeSkipped {
		t.Fatalf("b = %q, want skipped", run.StateOf("b").Status)
	}
}

func TestJoinCallableQuorumEscapeHatch(t *testing.T) {
	quorum2 := func(done, total int) bool { return done >= 2 }
	p := NewPlan("", nil)
	p.Add(
		&Node{ID: "start", Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) { return OK(nil), nil })},
		&Node{ID: "a", Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) { return OK("A"), nil })},
		&Node{ID: "b", Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) { return OK("B"), nil })},
		&Node{ID: "c", Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) { return OK("C"), nil })},
		&Node{ID: "done", Join: JoinQuorum(quorum2), Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			return OK("quorum-reached"), nil
		})},
	)
	p.Edge("start", "a", nil)
	p.Edge("start", "b", func(s map[string]any) bool { return false })
	p.Edge("start", "c", nil)
	p.Edge("a", "done", nil)
	p.Edge("b", "done", nil)
	p.Edge("c", "done", nil)
	run, err := NewScheduler(SchedulerOptions{}).Run(p, "t", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunCompleted {
		t.Fatalf("state = %q, want completed", run.State)
	}
	if run.FinalOutput != "quorum-reached" {
		t.Fatalf("final = %v, want quorum-reached", run.FinalOutput)
	}
	if run.StateOf("b").Status != NodeSkipped {
		t.Fatalf("b = %q, want skipped", run.StateOf("b").Status)
	}
}

func TestActivatedBypassIsOneShotNotPermanent(t *testing.T) {
	p := NewPlan("", nil)
	p.Add(
		&Node{ID: "slow", Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) { return OK("S"), nil })},
		&Node{ID: "gate", Join: JoinAll(), Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) { return OK("G"), nil })},
	)
	p.Edge("slow", "gate", nil)
	run, err := StartRun(p, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	run.Rearm("gate", true)
	if !containsStr(p.Ready(run, false), "gate") {
		t.Fatal("gate should be ready right after an immediate rearm")
	}
	run.MarkRunning("gate")
	run.MarkCompleted("gate", "G-first")
	run.Rearm("gate", false)
	if containsStr(p.Ready(run, false), "gate") {
		t.Fatal("the activated bypass is one-shot; gate must wait for slow now")
	}
	run.MarkRunning("slow")
	run.MarkCompleted("slow", "S")
	if !containsStr(p.Ready(run, false), "gate") {
		t.Fatal("gate should be ready once slow has completed")
	}
}

func TestFailedInstanceFailsTheRun(t *testing.T) {
	p := NewPlan("", map[string]Channel{"outs": Append(nil)})
	p.Add(
		&Node{ID: "fan", Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			return []Command{
				Send{Template: "w", Payload: "ok", Key: "good"},
				Send{Template: "w", Payload: "bad", Key: "bad"},
			}, nil
		})},
		&Node{ID: "w", Template: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			if s, _ := x.(string); s == "bad" {
				return nil, fmt.Errorf("boom")
			}
			return OKDelta(nil, map[string]any{"outs": []any{x}}), nil
		})},
		&Node{ID: "join", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			return OK(sortedStrings(ctx.Shared()["outs"])), nil
		})},
	)
	p.Edge("w", "join", nil)
	run, err := NewScheduler(SchedulerOptions{}).Run(p, "t", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunFailed {
		t.Fatalf("state = %q, want failed", run.State)
	}
	if run.StateOf("w#bad").Status != NodeFailed {
		t.Fatalf("w#bad = %q, want failed", run.StateOf("w#bad").Status)
	}
	// 兄弟 Node 在 Run 停止前已结算为事实。
	if !reflect.DeepEqual(run.Shared["outs"], []any{"ok"}) {
		t.Fatalf("outs = %v, want [ok]", run.Shared["outs"])
	}
}

func TestToolBodyAndLlmBodyRungs(t *testing.T) {
	reg := newFakeRegistry().add("shout", func(args map[string]any, ctx *NodeContext) (any, error) {
		text, _ := args["text"].(string)
		return strings.ToUpper(text), nil
	})
	p := NewPlan("", nil)
	p.Add(
		&Node{ID: "prep", Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			return OK(map[string]any{"text": "quiet"}), nil // map 值即工具参数
		})},
		&Node{ID: "call", Body: &ToolBody{ToolName: "shout"}},
		&Node{ID: "ask", Terminal: true, Body: &LLMBody{Prompt: "say the word"}},
	)
	p.Edge("prep", "call", nil)
	p.Edge("call", "ask", nil)
	sch := NewScheduler(SchedulerOptions{Llm: newScriptedLlm("the word"), Tools: reg})
	run, err := sch.Run(p, "t", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.FinalOutput != "the word" {
		t.Fatalf("final = %v, want the word", run.FinalOutput)
	}
	result, ok := run.StateOf("call").Output.(ToolResult) // 取到受治理的 ToolResult 作为值
	if !ok {
		t.Fatalf("call output = %T, want ToolResult", run.StateOf("call").Output)
	}
	if !result.OK || result.Output != "QUIET" {
		t.Fatalf("tool result = %+v, want ok QUIET", result)
	}
}

func TestTwoCallsInOneExecutionGetDistinctIds(t *testing.T) {
	reg := newFakeRegistry().add("echo", func(args map[string]any, ctx *NodeContext) (any, error) {
		name, _ := args["name"].(string)
		return "hi " + name, nil
	})
	var results []ToolResult
	p := NewPlan("", nil)
	p.Add(&Node{ID: "n", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
		r1, err := ctx.CallTool("echo", map[string]any{"name": "a"})
		if err != nil {
			return nil, err
		}
		r2, err := ctx.CallTool("echo", map[string]any{"name": "b"})
		if err != nil {
			return nil, err
		}
		results = append(results, r1, r2)
		return OK("done"), nil
	})})
	sch := NewScheduler(SchedulerOptions{Tools: reg})
	run, err := sch.Run(p, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunCompleted {
		t.Fatalf("state = %q, want completed", run.State)
	}
	if len(results) != 2 || results[0].Output != "hi a" || results[1].Output != "hi b" {
		t.Fatalf("results = %+v", results)
	}
	if results[0].CallID == results[1].CallID {
		t.Fatal("one id per call, not per attempt")
	}
}
