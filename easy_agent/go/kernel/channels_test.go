package kernel

// Channel 与 reducer：并发写不丢数据；同一波内多个 Node 写同一个 Last channel 必须大声失败。
// （test_channels.py 的 Go 对应版本。）

import (
	"reflect"
	"testing"
)

// TestReducersFold 直接单测四种内置 Reducer 的纯函数语义：
// 只调 Channel 工厂 (Last/Append/Add/Merge) 拿到 Reducer，喂 (old, neu)，断言合并结果。
// 不经过 scheduler / WaveWrites / ApplyEvent，把"折叠规则"从"折叠时机"里剥出来单独锁死。
// 工厂函数括号里的 nil 是 Init（本用例用不到），Reducer 收到的 old/neu 才是被验证的对象。
func TestReducersFold(t *testing.T) {
	// Append：把 neu 追加到 old 后面 —— 消息历史 / 检索片段用这种通道。
	// old=[a], neu=[b,c] → [a,b,c]（满足结合 & 交换律，AllowMulti=true）。
	if got := Append(nil).Reducer([]any{"a"}, []any{"b", "c"}); !reflect.DeepEqual(got, []any{"a", "b", "c"}) {
		t.Fatalf("append fold = %v, want [a b c]", got)
	}
	// Add：数值累加 —— 成本 / token 计数用这种通道。
	// old=1, neu=2 → 3；toInt 兼容 int/float 两种数值形态。
	if got := Add(0).Reducer(1, 2); toInt(got) != 3 {
		t.Fatalf("add fold = %v, want 3", got)
	}
	// Last：last-write-wins —— 当前阶段 / 判定这类单值通道。
	// old="old", neu="new" → "new"；同波多写会被 CheckAmbiguous 拦下（见下面的用例）。
	if got := Last(nil).Reducer("old", "new"); got != "new" {
		t.Fatalf("last fold = %v, want new", got)
	}
	// Merge：map 按键合并 —— 聚合多路结构化结果。
	// old={a:1}, neu={b:2} → {a:1, b:2}；同键时 neu 覆盖 old。
	want := map[string]any{"a": 1, "b": 2}
	if got := Merge(nil).Reducer(map[string]any{"a": 1}, map[string]any{"b": 2}); !reflect.DeepEqual(got, want) {
		t.Fatalf("merge fold = %v, want %v", got, want)
	}
}

// TestWaveWritesCollectsInDeclaredChannels 验证 WaveWrites 的三步流水线在"合法"情形下能正常工作：
//
//	① Buffer：一波内不同节点对同一个 Append 通道重复写 —— 应能按到达顺序都记下来；
//	② CheckAmbiguous：这里只用了 Append/Add（AllowMulti=true），不应报错；
//	③ Drain + Reducer 折叠：把两条 items 写合成 [1,2]，把一条 n 写合成 1。
//
// 换句话说这个测试锁定的是"不丢数据 + 不假阳性"：并发写同 key 不会丢、也不会被误判为歧义。
func TestWaveWritesCollectsInDeclaredChannels(t *testing.T) {
	channels := map[string]Channel{"items": Append(nil), "n": Add(0)}
	t.Logf("声明通道: items=Append(AllowMulti=true), n=Add(AllowMulti=true)")

	w := NewWaveWrites(channels)

	// 模拟一波里两个节点并发各追加一段 —— 消息历史场景。
	if err := w.Buffer("items", []any{1}, "b1"); err != nil {
		t.Fatal(err)
	}
	t.Logf("Buffer: 节点 b1 往 items 追加 [1]")
	if err := w.Buffer("items", []any{2}, "b2"); err != nil {
		t.Fatal(err)
	}
	t.Logf("Buffer: 节点 b2 往 items 追加 [2]  ← append 满足交换律，同波多节点写合法")
	// 另一个通道 n 只被 b1 写了一次。
	if err := w.Buffer("n", 1, "b1"); err != nil {
		t.Fatal(err)
	}
	t.Logf("Buffer: 节点 b1 往 n 加 1")

	// 本用例全是 AllowMulti=true 的通道，不应报歧义。
	if err := w.CheckAmbiguous(); err != nil {
		t.Fatal(err)
	}
	t.Logf("CheckAmbiguous 通过：无 Last 通道被多节点写")

	// 手写一遍"波增量"折叠。
	// ⚠ 这里 base 从 folded[wr.Key] 的零值 nil 起步，与生产代码 Run.WaveDelta 略有差异：
	//    生产版会先取 ch.Empty 作为单位元起步（见 channels.go Channel.Empty 字段）。
	//    本用例里 appendReduce(nil, ...) 也能正常工作，因此结果一致；这里主要是验"能不能合"，
	//    不是验"从 Empty 起步"的那条纪律。
	folded := map[string]any{}
	drained := w.Drain()
	t.Logf("Drain 吐出的到达顺序: %v", drained)
	for _, wr := range drained {
		ch := channels[wr.Key]
		folded[wr.Key] = ch.Reducer(folded[wr.Key], wr.Value)
		t.Logf("  折叠 key=%q 增量=%v  → 当前 fold=%v", wr.Key, wr.Value, folded[wr.Key])
	}

	// items = [1,2]：两次 Append 按 buffer 到达顺序合并。
	if !reflect.DeepEqual(folded["items"], []any{1, 2}) {
		t.Fatalf("items = %v, want [1 2]", folded["items"])
	}
	// n = 1：一次 Add 写入 0+1 = 1。
	if toInt(folded["n"]) != 1 {
		t.Fatalf("n = %v, want 1", folded["n"])
	}
	t.Logf("断言通过：items=[1,2] (两次 append 依序合并)、n=1 (单次 add)")
}

// TestUndeclaredChannelRejected 验证 Buffer 的"写未声明通道 → 大声报错"纪律。
//
// 设计意图：只声明了通道 "a"，却尝试向 "b" 写 —— 几乎一定是拼写错误或设计遗漏。
// 与其默默丢弃造成下游少一块状态、还不如当场返回 error，把问题暴露在最早的位置。
func TestUndeclaredChannelRejected(t *testing.T) {
	w := NewWaveWrites(map[string]Channel{"a": Append(nil)})
	t.Logf("只声明了一个通道: a=Append；接下来故意向未声明的 b 写")
	err := w.Buffer("b", 1, "x")
	if err == nil {
		t.Fatal("writing an undeclared channel must fail loudly")
	}
	t.Logf("拦截成功：err = %v", err)
}

// TestLastChannelAmbiguousInOneWave 验证 Last 通道在"同一波被多个不同节点写"时
// 会当场报 *AmbiguousWriteError（而不是默默以调度顺序决定结果）。
//
// 设计意图：Last 是 last-write-wins，结果取决于"谁是最后一个"；并发下调度顺序不可预测，
// 默默合造就"同代码不同环境不同结果"。CheckAmbiguous 把这条隐患前置到一波屏障处，
// 逼开发者换用 Append/Add/Merge 或限定只有一个节点写。
func TestLastChannelAmbiguousInOneWave(t *testing.T) {
	w := NewWaveWrites(map[string]Channel{"v": Last(nil)})
	t.Logf("声明通道: v=Last(AllowMulti=false)")
	// Buffer 本身不报歧义（它只"记账"）；故意忽略 err 因为这里一定写不会失败。
	_ = w.Buffer("v", 1, "a")
	t.Logf("Buffer: 节点 a 写 v=1")
	_ = w.Buffer("v", 2, "b")
	t.Logf("Buffer: 节点 b 写 v=2  ← 同波同 Last 通道多节点写，预期拦下")

	err := w.CheckAmbiguous()
	if err == nil {
		t.Fatal("a last channel written twice in one wave must be ambiguous")
	}
	t.Logf("拦到错：err = %v", err)

	// 验证错的"类型"而不只是非空 —— 上层能依赖这个具体类型做分类处理。
	if _, ok := err.(*AmbiguousWriteError); !ok {
		t.Fatalf("error = %T, want *AmbiguousWriteError", err)
	}
	t.Logf("断言通过：错误类型恰为 *AmbiguousWriteError")
}
