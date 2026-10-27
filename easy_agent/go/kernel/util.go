package kernel

// 内核 reducer 与 fold 共用的小型数值/强制转换辅助函数。
// 它们之所以存在，是因为 Go 的静态类型仍须折叠经状态 channel 传输的
// 动态 `any` 值（且经 JSON 往返后，int 可能以 float64 到达）。

// isIntish 报告 v 是否为整数值（nil 视为 0）。
func isIntish(v any) bool {
	switch v.(type) {
	case nil, int, int64, int32, int16, int8, uint, uint64, uint32, uint16, uint8:
		return true
	}
	return false
}

// toInt 将数值强制为 int（nil -> 0）。
func toInt(v any) int {
	switch n := v.(type) {
	case nil:
		return 0
	case int:
		return n
	case int64:
		return int(n)
	case int32:
		return int(n)
	case float64:
		return int(n)
	case float32:
		return int(n)
	case uint:
		return int(n)
	case uint64:
		return int(n)
	}
	return 0
}

// toFloat 将数值强制为 float64，并报告其是否为数值。
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case nil:
		return 0, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint64:
		return float64(n), true
	}
	return 0, false
}

// asList 将值归一化为 []any（nil -> 空，list -> 自身）。
func asList(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return nil
}

// copyList 返回 []any 的浅拷贝。
func copyList(in []any) []any {
	out := make([]any, len(in))
	copy(out, in)
	return out
}

// copyMap 返回 map[string]any 的浅拷贝。
func copyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
