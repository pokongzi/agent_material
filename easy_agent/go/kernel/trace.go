package kernel

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// trace —— 把 Event 流投影成 Run 的因果树。
//
// 与 state 和制品一样，trace 不是第二套系统：每个 Event 已携带 run_id、单调
// seq 与 parent_id（子 Run 的事件指向其父）。折叠这些字段，整个执行——父 Run
// 及其委派的子 agent——便成一棵树：
//
//	BuildTrace(events) -> nested Span objects (one per Run, children attached)
//	RenderTrace(events) -> an indented, human-readable trace (CLI / teaching)
//
// 一个 Span 就是一个 Run：其有序事实加上它派生的 Run。这是零依赖的可观测性
// 核心；把同一因果结构导出到 OpenTelemetry 只是挂在 Bus 上的适配器，无须改这里。

// Span 是作为 Trace 节点的一个 Run：identity、其事实，以及它派生的 Run。
type Span struct {
	RunID    string
	Name     string
	Task     string
	Events   []Event
	Children []*Span
	StartTs  float64
	EndTs    float64
	Status   string // ok | error | suspended
}

// Label 是人类可读的标题："name — task"（未命名时用 Run ID）。
func (s *Span) Label() string {
	who := s.Name
	if who == "" {
		who = s.RunID
	}
	if s.Task != "" {
		return who + " — " + s.Task
	}
	return who
}

// DurationMS 是 Run 的墙钟时长，整毫秒。
func (s *Span) DurationMS() int {
	ms := int(math.Round((s.EndTs - s.StartTs) * 1000))
	if ms < 0 {
		return 0
	}
	return ms
}

// BuildTrace 把扁平（可能含多个 Run）的 Event 列表归组为 Span 树。
// 父子关系取自各 Run 的 RUN_STARTED 事件 parent_id；父不存在或未知的 Run
// 作为根返回。
func BuildTrace(events []Event) []*Span {
	byRun := map[string][]Event{}
	var order []string // Run 首见顺序，保证根/子的确定性
	for _, ev := range events {
		if _, seen := byRun[ev.RunID]; !seen {
			order = append(order, ev.RunID)
		}
		byRun[ev.RunID] = append(byRun[ev.RunID], ev)
	}

	spans := map[string]*Span{}
	parents := map[string]string{}
	for _, runID := range order {
		ordered := sortEvents(byRun[runID])
		var started *Event
		for i := range ordered {
			if ordered[i].Kind == EvRunStarted {
				started = &ordered[i]
				break
			}
		}
		data := map[string]any{}
		if started != nil {
			parents[runID] = started.ParentID
			data = started.Data
		}
		status := "ok"
		hasFailed, hasInterrupted := false, false
		for _, e := range ordered {
			if e.Kind == EvRunFailed {
				hasFailed = true
			}
			if e.Kind == EvInterrupted {
				hasInterrupted = true
			}
		}
		if hasFailed {
			status = "error"
		} else if hasInterrupted {
			status = "suspended"
		}
		name, _ := data["name"].(string)
		task, _ := data["task"].(string)
		spans[runID] = &Span{
			RunID:   runID,
			Name:    name,
			Task:    task,
			Events:  ordered,
			StartTs: ordered[0].Ts,
			EndTs:   ordered[len(ordered)-1].Ts,
			Status:  status,
		}
	}

	var roots []*Span
	for _, runID := range order {
		span := spans[runID]
		if pid := parents[runID]; pid != "" {
			if parent, ok := spans[pid]; ok {
				parent.Children = append(parent.Children, span)
				continue
			}
		}
		roots = append(roots, span)
	}
	return roots
}

// sortEvents 返回按 seq 排序的稳定副本。
func sortEvents(in []Event) []Event {
	out := make([]Event, len(in))
	copy(out, in)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

// briefEvent 为 Trace 视图渲染一条事实的一行摘要。
func briefEvent(ev Event) string {
	d := ev.Data
	switch ev.Kind {
	case EvNodeStarted, EvNodeCompleted, EvNodeSkipped:
		return dataStr(d, "node")
	case EvNodeFailed:
		return dataStr(d, "node") + "  " + dataStr(d, "error")
	case EvControl:
		target := dataStr(d, "target")
		if target == "" {
			target = dataStr(d, "template")
		}
		return strings.TrimSpace(dataStr(d, "op") + " " + target)
	case EvDelegated:
		return "spawn " + dataStr(d, "child_run_id")
	case EvArtifactWritten:
		return fmt.Sprintf("%s v%v", dataStr(d, "filename"), d["version"])
	case EvInterrupted:
		parked, _ := d["parked"].(map[string]any)
		keys := make([]string, 0, len(parked))
		for k := range parked {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		quoted := make([]string, len(keys))
		for i, k := range keys {
			quoted[i] = "'" + k + "'"
		}
		return "park [" + strings.Join(quoted, ", ") + "]"
	}
	return ""
}

// RenderTrace 把 Run 及其事实缩进成可读的 Trace（CLI 视图）。
func RenderTrace(events []Event) string {
	var lines []string
	var walk func(span *Span, depth int)
	walk = func(span *Span, depth int) {
		pad := strings.Repeat("  ", depth)
		lines = append(lines, pad+"● "+span.Label())
		for _, ev := range span.Events {
			if ev.Kind == EvRunStarted {
				continue // Span 头部已写明这个 Run 是谁
			}
			line := fmt.Sprintf("%s  %3d %-17s %s", pad, ev.Seq, ev.Kind, briefEvent(ev))
			lines = append(lines, strings.TrimRight(line, " "))
		}
		for _, child := range span.Children {
			walk(child, depth+1)
		}
	}
	for _, root := range BuildTrace(events) {
		walk(root, 0)
	}
	return strings.Join(lines, "\n")
}
