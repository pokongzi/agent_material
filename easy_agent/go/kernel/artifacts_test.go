package kernel

// 制品：字节进 BlobStore，指针事实进事件流，制品库只是投影——与 state 同一条定律。
// （test_artifacts.py 内核部分用例的 Go 对应版本。）

import (
	"fmt"
	"strings"
	"testing"
)

// writerPlan 构建单 Node 的 "writer" Plan，Body 为 fn（对应 Python 侧 _plan_with）。
func writerPlan(fn Fn) *Plan {
	p := NewPlan("writer", nil)
	p.Add(&Node{ID: "w", Terminal: true, Body: NewFnBody(fn)})
	return p
}

// artifactWritten 构造裸的 ARTIFACT_WRITTEN 指针事实，供折叠测试用。
func artifactWritten(seq int, filename string, version int) Event {
	return NewEvent(seq, "run-x", EvArtifactWritten, map[string]any{
		"filename": filename, "version": version,
		"uri": fmt.Sprintf("u%d", seq), "mime": "", "size": 1,
	}, "")
}

func TestArtifactSavedAndProjected(t *testing.T) {
	p := writerPlan(func(x any, ctx *NodeContext) (any, error) {
		pointer, err := ctx.SaveArtifact("report.md", "# Hello\n\nworld", "", "Report")
		if err != nil {
			return nil, err
		}
		return fmt.Sprintf("saved v%d", pointer["version"]), nil
	})
	sch := NewScheduler(SchedulerOptions{})
	run, err := sch.Run(p, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.FinalOutput != "saved v1" {
		t.Fatalf("final = %v, want saved v1", run.FinalOutput)
	}

	events, _ := sch.EventLog().Events(run.RunID)
	latest, err := LatestArtifacts(events)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest) != 1 || latest["report.md"] == nil {
		t.Fatalf("latest = %v, want only report.md", latest)
	}
	pointer := latest["report.md"]
	if toInt(pointer["version"]) != 1 {
		t.Fatalf("version = %v, want 1", pointer["version"])
	}
	if pointer["mime"] != "text/markdown" { // 由 .md 扩展名推断
		t.Fatalf("mime = %v, want text/markdown", pointer["mime"])
	}
	if toInt(pointer["size"]) != len("# Hello\n\nworld") {
		t.Fatalf("size = %v, want %d", pointer["size"], len("# Hello\n\nworld"))
	}
	if pointer["title"] != "Report" {
		t.Fatalf("title = %v, want Report", pointer["title"])
	}
	// 字节经 BlobStore 往返，不走事件流。
	body, err := sch.Blobs().Load(pointer["uri"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "# Hello\n\nworld" {
		t.Fatalf("bytes = %q", string(body))
	}
}

func TestArtifactVersionsIncrement(t *testing.T) {
	p := writerPlan(func(x any, ctx *NodeContext) (any, error) {
		if _, err := ctx.SaveArtifact("f.txt", "one", "", ""); err != nil {
			return nil, err
		}
		if _, err := ctx.SaveArtifact("f.txt", "two", "", ""); err != nil {
			return nil, err
		}
		return "ok", nil
	})
	sch := NewScheduler(SchedulerOptions{})
	run, err := sch.Run(p, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	events, _ := sch.EventLog().Events(run.RunID)
	byName, err := ArtifactsFromEvents(events)
	if err != nil {
		t.Fatal(err)
	}
	versions := byName["f.txt"]
	if len(versions) != 2 || toInt(versions[0]["version"]) != 1 || toInt(versions[1]["version"]) != 2 {
		t.Fatalf("versions = %v, want [1 2]", versions)
	}
	b0, _ := sch.Blobs().Load(versions[0]["uri"].(string))
	b1, _ := sch.Blobs().Load(versions[1]["uri"].(string))
	if string(b0) != "one" || string(b1) != "two" {
		t.Fatalf("bytes = %q / %q", string(b0), string(b1))
	}
	latest, _ := LatestArtifacts(events)
	if toInt(latest["f.txt"]["version"]) != 2 { // latest 投影指向 v2
		t.Fatalf("latest = %v, want 2", latest["f.txt"]["version"])
	}
}

func TestDictArtifactSerializedAsJson(t *testing.T) {
	p := writerPlan(func(x any, ctx *NodeContext) (any, error) {
		_, err := ctx.SaveArtifact("data.json", map[string]any{"a": 1, "b": []any{2, 3}}, "", "")
		return "ok", err
	})
	sch := NewScheduler(SchedulerOptions{})
	run, err := sch.Run(p, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	events, _ := sch.EventLog().Events(run.RunID)
	latest, _ := LatestArtifacts(events)
	pointer := latest["data.json"]
	if pointer["mime"] != "application/json" {
		t.Fatalf("mime = %v, want application/json", pointer["mime"])
	}
	body, _ := sch.Blobs().Load(pointer["uri"].(string))
	if !strings.Contains(string(body), `"a": 1`) {
		t.Fatalf("body = %q, want it to contain \"a\": 1", string(body))
	}
}

func TestCustomBlobStoreIsUsed(t *testing.T) {
	// 调用方注入的 BlobStore 接收字节（依赖注入）。
	p := writerPlan(func(x any, ctx *NodeContext) (any, error) {
		_, err := ctx.SaveArtifact("x.txt", "hi", "", "")
		return "ok", err
	})
	blobs := NewInMemoryBlobStore()
	sch := NewScheduler(SchedulerOptions{Blobs: blobs})
	run, err := sch.Run(p, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	events, _ := sch.EventLog().Events(run.RunID)
	latest, _ := LatestArtifacts(events)
	body, err := blobs.Load(latest["x.txt"]["uri"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hi" {
		t.Fatalf("body = %q, want hi", string(body))
	}
}

func TestVersionGapAfterDeleteNeverOverwrites(t *testing.T) {
	// nextVersion 取 max(现存)+1 而非计数：删掉中间版本后，下次保存不得撞号覆盖更高版本。
	blobs := NewInMemoryBlobStore()
	for _, text := range []string{"one", "two", "three"} {
		if _, err := blobs.Save("r", "f.txt", text, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := blobs.Delete("r/f.txt.v2"); err != nil { // 留下版本空洞
		t.Fatal(err)
	}
	pointer, err := blobs.Save("r", "f.txt", "four", "")
	if err != nil {
		t.Fatal(err)
	}
	if toInt(pointer["version"]) != 4 { // 按计数 +1 会得到 3
		t.Fatalf("version = %v, want 4", pointer["version"])
	}
	body, _ := blobs.Load("r/f.txt.v3")
	if string(body) != "three" { // 未被改动
		t.Fatalf("v3 = %q, want three", string(body))
	}
}

func TestArtifactReadBackAcrossNodes(t *testing.T) {
	// 一个 Node 保存、下一个 Node 读取：指针从本次 Run 的事实折叠而来（事件流是唯一索引），字节取自存储。
	p := NewPlan("roundtrip", nil)
	p.Add(
		&Node{ID: "save", Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			_, err := ctx.SaveArtifact("report.md", "# Title\n\nbody", "", "")
			return "saved", err
		})},
		&Node{ID: "load", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			body, err := ctx.LoadArtifact("report.md", 0)
			if err != nil {
				return nil, err
			}
			return strings.SplitN(string(body), "\n", 2)[0], nil
		})},
	)
	p.Edge("save", "load", nil)
	run, err := NewScheduler(SchedulerOptions{}).Run(p, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.FinalOutput != "# Title" {
		t.Fatalf("final = %v, want # Title", run.FinalOutput)
	}
}

func TestLoadArtifactPinsVersionsAndReportsMissing(t *testing.T) {
	p := writerPlan(func(x any, ctx *NodeContext) (any, error) {
		if _, err := ctx.SaveArtifact("f.txt", "one", "", ""); err != nil {
			return nil, err
		}
		if _, err := ctx.SaveArtifact("f.txt", "two", "", ""); err != nil {
			return nil, err
		}
		latest, err := ctx.LoadArtifact("f.txt", 0)
		if err != nil {
			return nil, err
		}
		pinned, err := ctx.LoadArtifact("f.txt", 1)
		if err != nil {
			return nil, err
		}
		if _, err := ctx.LoadArtifact("f.txt", 9); err == nil {
			return nil, fmt.Errorf("version 9 should be missing")
		}
		if _, err := ctx.LoadArtifact("missing.txt", 0); err == nil {
			return nil, fmt.Errorf("missing.txt should be absent")
		}
		return string(latest) + "/" + string(pinned), nil
	})
	run, err := NewScheduler(SchedulerOptions{}).Run(p, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.FinalOutput != "two/one" {
		t.Fatalf("final = %v, want two/one", run.FinalOutput)
	}
}

func TestToolReadsAnArtifactBack(t *testing.T) {
	// 工具拿到的是同一个 NodeContext，所以"一个工具抓取并保存、后面的工具 grep 字节"
	// 可直接组合，无需新管道。
	reg := newFakeRegistry().
		add("fetch", func(args map[string]any, ctx *NodeContext) (any, error) {
			if _, err := ctx.SaveArtifact("logs.txt", "line1 ERROR boom\nline2 ok", "", ""); err != nil {
				return nil, err
			}
			name, _ := args["name"].(string)
			return "saved logs.txt for " + name, nil
		}).
		add("grep", func(args map[string]any, ctx *NodeContext) (any, error) {
			body, err := ctx.LoadArtifact("logs.txt", 0)
			if err != nil {
				return nil, err
			}
			pattern, _ := args["pattern"].(string)
			var kept []string
			for _, line := range strings.Split(string(body), "\n") {
				if strings.Contains(line, pattern) {
					kept = append(kept, line)
				}
			}
			return strings.Join(kept, "\n"), nil
		})

	p := writerPlan(func(x any, ctx *NodeContext) (any, error) {
		if _, err := ctx.CallTool("fetch", map[string]any{"name": "payments"}); err != nil {
			return nil, err
		}
		return ctx.CallTool("grep", map[string]any{"pattern": "ERROR"})
	})
	run, err := NewScheduler(SchedulerOptions{Tools: reg}).Run(p, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	result, ok := run.FinalOutput.(ToolResult)
	if !ok {
		t.Fatalf("final = %T, want ToolResult", run.FinalOutput)
	}
	if result.Output != "line1 ERROR boom" {
		t.Fatalf("output = %v, want line1 ERROR boom", result.Output)
	}
}

func TestReadBackSurvivesResume(t *testing.T) {
	// 字节在挂起前已保存；resume 重放事实后继续，读取方从重建的事件流折叠指针——没有第二个存储可丢。
	p := NewPlan("resume-read", nil)
	p.Add(
		&Node{ID: "draft", Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			if ctx.ResumeValue == nil {
				if _, err := ctx.SaveArtifact("draft.md", "draft words", "", ""); err != nil {
					return nil, err
				}
				return Park("review", nil, ""), nil
			}
			return ctx.ResumeValue, nil
		})},
		&Node{ID: "polish", Terminal: true, Body: NewFnBody(func(x any, ctx *NodeContext) (any, error) {
			body, err := ctx.LoadArtifact("draft.md", 0)
			if err != nil {
				return nil, err
			}
			return strings.ToUpper(string(body)), nil
		})},
	)
	p.Edge("draft", "polish", nil)
	sch := NewScheduler(SchedulerOptions{})
	run, err := sch.Run(p, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunSuspended {
		t.Fatalf("state = %q, want suspended", run.State)
	}
	run2, err := sch.Resume(p, run.RunID, "approved", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if run2.FinalOutput != "DRAFT WORDS" {
		t.Fatalf("final = %v, want DRAFT WORDS", run2.FinalOutput)
	}
}

func TestFoldRefusesRegressedVersions(t *testing.T) {
	// 存储分配的版本号乱序到达会让 "latest" 说谎；折叠拒绝放行。
	events := []Event{artifactWritten(3, "f.txt", 2), artifactWritten(4, "f.txt", 1)}
	if _, err := ArtifactsFromEvents(events); err == nil || !strings.Contains(err.Error(), "strictly increase") {
		t.Fatalf("err = %v, want it to mention strictly increase", err)
	}
}

func TestFoldAllowsSparseVersions(t *testing.T) {
	// 字节落库与事实提交之间崩溃会跳号——稀疏但递增是健康历史，不是分叉。
	events := []Event{artifactWritten(3, "f.txt", 1), artifactWritten(4, "f.txt", 3)}
	byName, err := ArtifactsFromEvents(events)
	if err != nil {
		t.Fatal(err)
	}
	folded := byName["f.txt"]
	if len(folded) != 2 || toInt(folded[0]["version"]) != 1 || toInt(folded[1]["version"]) != 3 {
		t.Fatalf("folded = %v, want [1 3]", folded)
	}
	latest, _ := LatestArtifacts(events)
	if toInt(latest["f.txt"]["version"]) != 3 {
		t.Fatalf("latest = %v, want 3", latest["f.txt"]["version"])
	}
}
