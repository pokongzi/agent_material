package kernel

// Channel 与 reducer：并发写不丢数据；同一波内多个 Node 写同一个 Last channel 必须大声失败。
// （test_channels.py 的 Go 对应版本。）

import (
	"reflect"
	"testing"
)

func TestReducersFold(t *testing.T) {
	if got := Append(nil).Reducer([]any{"a"}, []any{"b", "c"}); !reflect.DeepEqual(got, []any{"a", "b", "c"}) {
		t.Fatalf("append fold = %v, want [a b c]", got)
	}
	if got := Add(0).Reducer(1, 2); toInt(got) != 3 {
		t.Fatalf("add fold = %v, want 3", got)
	}
	if got := Last(nil).Reducer("old", "new"); got != "new" {
		t.Fatalf("last fold = %v, want new", got)
	}
	want := map[string]any{"a": 1, "b": 2}
	if got := Merge(nil).Reducer(map[string]any{"a": 1}, map[string]any{"b": 2}); !reflect.DeepEqual(got, want) {
		t.Fatalf("merge fold = %v, want %v", got, want)
	}
}

func TestWaveWritesCollectsInDeclaredChannels(t *testing.T) {
	channels := map[string]Channel{"items": Append(nil), "n": Add(0)}
	w := NewWaveWrites(channels)
	if err := w.Buffer("items", []any{1}, "b1"); err != nil {
		t.Fatal(err)
	}
	if err := w.Buffer("items", []any{2}, "b2"); err != nil {
		t.Fatal(err)
	}
	if err := w.Buffer("n", 1, "b1"); err != nil {
		t.Fatal(err)
	}
	if err := w.CheckAmbiguous(); err != nil {
		t.Fatal(err)
	}
	folded := map[string]any{}
	for _, wr := range w.Drain() {
		ch := channels[wr.Key]
		folded[wr.Key] = ch.Reducer(folded[wr.Key], wr.Value)
	}
	if !reflect.DeepEqual(folded["items"], []any{1, 2}) {
		t.Fatalf("items = %v, want [1 2]", folded["items"])
	}
	if toInt(folded["n"]) != 1 {
		t.Fatalf("n = %v, want 1", folded["n"])
	}
}

func TestUndeclaredChannelRejected(t *testing.T) {
	w := NewWaveWrites(map[string]Channel{"a": Append(nil)})
	if err := w.Buffer("b", 1, "x"); err == nil {
		t.Fatal("writing an undeclared channel must fail loudly")
	}
}

func TestLastChannelAmbiguousInOneWave(t *testing.T) {
	w := NewWaveWrites(map[string]Channel{"v": Last(nil)})
	_ = w.Buffer("v", 1, "a")
	_ = w.Buffer("v", 2, "b")
	err := w.CheckAmbiguous()
	if err == nil {
		t.Fatal("a last channel written twice in one wave must be ambiguous")
	}
	if _, ok := err.(*AmbiguousWriteError); !ok {
		t.Fatalf("error = %T, want *AmbiguousWriteError", err)
	}
}
