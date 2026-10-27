package kernel

// Bus 的三条协议：观察不影响主流，检查 fail-closed，有界订阅施加背压。
// （test_bus.py + test_backpressure.py 的 Go 对应版本。）

import (
	"testing"
	"time"
)

func TestFireObserverErrorIsSwallowed(t *testing.T) {
	bus := NewBus()
	boom := func(map[string]any) { panic("observer exploded") }
	var seen []map[string]any
	bus.On("e", boom)
	bus.On("e", func(data map[string]any) { seen = append(seen, data) })

	bus.Fire("e", map[string]any{"x": 1}) // 不得 panic

	if len(seen) != 1 || seen[0]["x"] != 1 {
		t.Fatalf("seen = %v, want [{x:1}]", seen)
	}
}

func TestCheckDenyWins(t *testing.T) {
	bus := NewBus()
	bus.AddChecker("gate", func(map[string]any) (BlockingResult, error) {
		return BlockingResult{Allowed: true}, nil
	})
	bus.AddChecker("gate", func(map[string]any) (BlockingResult, error) {
		return BlockingResult{Allowed: false, Reason: "no"}, nil
	})
	if verdict := bus.Check("gate", nil); verdict.Allowed {
		t.Fatal("any veto must block")
	}
}

func TestCheckFailClosedWhenCheckerRaises(t *testing.T) {
	bus := NewBus()
	bus.AddChecker("gate", func(map[string]any) (BlockingResult, error) {
		panic("checker itself broke")
	})
	if verdict := bus.Check("gate", nil); verdict.Allowed {
		t.Fatal("a broken checker must never mean allow")
	}
}

func TestBlockSubscriptionPushesBackProducer(t *testing.T) {
	bus := NewBus()
	sub, err := bus.Subscribe([]string{"token"}, 1, "block")
	if err != nil {
		t.Fatal(err)
	}
	bus.Fire("token", map[string]any{"i": 0}) // 第一帧入队，队列已满

	// 无人消费时，投递第二帧必须阻塞生产者。
	blocked := make(chan struct{})
	go func() {
		bus.Fire("token", map[string]any{"i": 1})
		close(blocked)
	}()
	select {
	case <-blocked:
		t.Fatal("a full blocking subscription must push back on the producer")
	case <-time.After(100 * time.Millisecond):
	}

	// 消费一帧腾出槽位，放行被阻塞的生产者。
	item, ok := sub.Get()
	if !ok || item["i"] != 0 {
		t.Fatalf("got %v (ok=%v), want i=0", item, ok)
	}
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("the producer stayed blocked after a slot freed")
	}
}

func TestDropSubscriptionNeverBlocksAndCounts(t *testing.T) {
	bus := NewBus()
	sub, err := bus.Subscribe([]string{"token"}, 1, "drop")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		bus.Fire("token", map[string]any{"i": i}) // 永不阻塞
	}
	if sub.Dropped() != 3 {
		t.Fatalf("dropped = %d, want 3", sub.Dropped())
	}
	item, ok := sub.Get()
	if !ok || item["i"] != 0 {
		t.Fatalf("got %v (ok=%v), want i=0", item, ok)
	}
}

func TestCloseUnregistersSubscription(t *testing.T) {
	bus := NewBus()
	sub, err := bus.Subscribe([]string{"x"}, 0, "block")
	if err != nil {
		t.Fatal(err)
	}
	if len(bus.subscriptions) != 1 {
		t.Fatalf("subscriptions = %d, want 1", len(bus.subscriptions))
	}
	sub.Close()
	if len(bus.subscriptions) != 0 {
		t.Fatalf("subscriptions = %d after close, want 0", len(bus.subscriptions))
	}
}

func TestNodeCanStreamEventsThroughContext(t *testing.T) {
	// Node 用 ctx.Emit 逐块流式发事件，订阅者按序收到（最小流式循环，由真实调度器驱动）。
	bus := NewBus()
	sub, err := bus.Subscribe([]string{"token"}, 0, "block")
	if err != nil {
		t.Fatal(err)
	}
	p := NewPlan("", nil)
	p.Add(&Node{ID: "s", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
		for _, piece := range []string{"你", "好", "呀"} {
			ctx.Emit("token", map[string]any{"piece": piece})
		}
		return "完成", nil
	})})
	sch := NewScheduler(SchedulerOptions{Bus: bus})
	run, err := sch.Run(p, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.FinalOutput != "完成" {
		t.Fatalf("final = %v, want 完成", run.FinalOutput)
	}
	var got []any
	for i := 0; i < 3; i++ {
		item, ok := sub.Get()
		if !ok {
			break
		}
		got = append(got, item["piece"])
	}
	sub.Close()
	want := []any{"你", "好", "呀"}
	if len(got) != len(want) {
		t.Fatalf("received %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("received %v, want %v", got, want)
		}
	}
}
