package kernel

// 测试专用夹具：离线、确定性的端口（DIP），让整个内核流水线无需模型厂商或具体工具实现即可运行。
// 它们是 runtime.ScriptedLlm 与 runtime.ToolRegistry 的 Go 对应版本，裁剪到内核测试所需
// （受治理的流水线在 runtime 层）。

import (
	"fmt"
	"sort"
	"sync"
)

// scriptedLlm 按预设脚本作答，每次 Chat 消耗一项。脚本项与 Python 原版支持同样的简写：
//   - 普通字符串：文本回答；
//   - ToolCall / []ToolCall：请求调用工具；
//   - LlmReply：原样返回（最完整的形式）。
//
// 每个非 LlmReply 的回答都带 tokens=6，便于可预测计费。
type scriptedLlm struct {
	mu           sync.Mutex
	script       []any
	messagesSeen [][]map[string]any
}

func newScriptedLlm(script ...any) *scriptedLlm {
	return &scriptedLlm{script: script}
}

func (l *scriptedLlm) Chat(messages []map[string]any, opt LlmOptions) (LlmReply, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cp := make([]map[string]any, len(messages))
	copy(cp, messages)
	l.messagesSeen = append(l.messagesSeen, cp)
	if len(l.script) == 0 {
		return LlmReply{Text: "(script exhausted)"}, nil
	}
	item := l.script[0]
	l.script = l.script[1:]
	switch v := item.(type) {
	case LlmReply:
		return v, nil
	case ToolCall:
		return LlmReply{ToolCalls: []ToolCall{v}, Tokens: 6}, nil
	case []ToolCall:
		return LlmReply{ToolCalls: v, Tokens: 6}, nil
	default:
		return LlmReply{Text: fmt.Sprintf("%v", v), Tokens: 6}, nil
	}
}

// toolFn 是一个已注册的工具实现：入参为 arguments，返回值即结果。
type toolFn func(args map[string]any, ctx *NodeContext) (any, error)

// fakeRegistry 是内核测试用的最小 ToolPort：name -> function 表，统一包成 ToolResult。
// 未知名字或抛出的错误一律返回失败的 ToolResult（feedback），绝不作为结构性错误。
type fakeRegistry struct {
	tools map[string]toolFn
}

func newFakeRegistry() *fakeRegistry { return &fakeRegistry{tools: map[string]toolFn{}} }

func (r *fakeRegistry) add(name string, fn toolFn) *fakeRegistry {
	r.tools[name] = fn
	return r
}

func (r *fakeRegistry) Dispatch(call ToolCall, ctx *NodeContext) (ToolResult, error) {
	fn, ok := r.tools[call.Name]
	if !ok {
		return ToolFailure("no such tool: "+call.Name, call.CallID), nil
	}
	out, err := fn(call.Arguments, ctx)
	if err != nil {
		return ToolFailure(err.Error(), call.CallID), nil
	}
	return ToolSuccess(out, call.CallID), nil
}

// sortedStrings 把 []any 中的字符串排序后仍以 []any 返回，让断言不依赖波内并发顺序。
func sortedStrings(v any) []any {
	items := asList(v)
	strs := make([]string, 0, len(items))
	for _, it := range items {
		if s, ok := it.(string); ok {
			strs = append(strs, s)
		}
	}
	sort.Strings(strs)
	out := make([]any, len(strs))
	for i, s := range strs {
		out[i] = s
	}
	return out
}

// sortedIntsAny 把 []any 中的数字排序后仍以 []any 返回。
func sortedIntsAny(v any) []any {
	items := asList(v)
	nums := make([]int, 0, len(items))
	for _, it := range items {
		nums = append(nums, toInt(it))
	}
	sort.Ints(nums)
	out := make([]any, len(nums))
	for i, n := range nums {
		out[i] = n
	}
	return out
}

// sortedStrs 是简单的 []string 排序辅助函数，用于实例 key 的断言。
func sortedStrs(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	sort.Strings(out)
	return out
}
