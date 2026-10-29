# kernel — 一个教学级 Agent 执行内核（文件导览）

`prodagent/go/kernel` 是一个**零第三方依赖、只用 Go 标准库**的 agent 执行内核。
它的目标不是"能跑"，而是把构建 Agent 的几个核心思想拆成**互相独立、可单独读懂**的小文件，方便逐个学习：

- **事件溯源（Event Sourcing）**：`Event` 是唯一真相源，状态 / 制品 / trace 都是事件流的"投影"。
- **波引擎（Wave Engine）**：节点按"波"并发执行，波末统一结算，保证并发结果确定。
- **依赖倒置（DIP / 端口）**：内核不认识 OpenAI、向量库、具体工具，只依赖接口，外部注入实现。
- **控制与数据分离**：节点只能返回 `Goto` / `Send` 两个控制命令，数据变更走 `StateDelta` + reducer。

> 这份 README 只讲**每个文件是干什么的、该怎么读**。想动手验证，直接 `go test ./kernel/` 即可。

---

## 一、推荐阅读顺序

先记住一个**三句话的故事**，所有文件都是故事里的一个角色：

1. `Plan` 是**图纸** —— 声明有哪些节点、谁连向谁；
2. `Scheduler` 是**引擎** —— 一批一批地跑节点（"波"）；
3. 跑过的每一步都记成不可篡改的 `Event` —— 状态、轨迹、制品全部能从事件流重算出来。

然后按"**回答一个问题**"的顺序读，每个文件解决一件事：

```
graph.go      图纸长什么样？（节点 / 边 / Plan）
body.go       节点里跑的代码长什么样？（统一接口 + 四种内置实现）
scheduler.go  谁来推进执行？（波引擎：算就绪 → 并发跑 → 屏障结算）
command.go    节点如何影响流程？（只有 Goto / Send 两个命令）
channels.go   并发写同一状态怎么办？（reducer 在波末合并）
run.go        执行进度记在哪？（状态机；Apply 是唯一写入口）
eventlog.go   历史记在哪？（Event 是事实之源）
bus.go        外界如何观察 / 拦截？（Fire 观察、Check 裁决、Subscribe 订阅）
ports.go      模型和工具从哪来？（接口边界，外部注入实现）
replay / trace / blob   同一批事件的三种读法（重建 / 画树 / 取文件）
```

`types.go`（公共值类型）和 `util.go`（数值小工具）遇到再查即可。
读到中途累了，回到三句话故事里定位自己在哪，再继续。

---

## 二、每个源文件的作用

### 第 1 层：不可变值与合并规则

| 文件 | 一句话作用 | 关键内容 |
|---|---|---|
| **`types.go`** | 定义内核最基础的值类型，是一切的起点 | `NodeStatus`（pending/running/completed/skipped/failed）、`RunState`（Run 生命周期的 4 个状态）、`ToolCall` / `ToolResult`（`ToolSuccess` / `ToolFailure`） |
| **`command.go`** | 节点与 scheduler 对话的**唯一控制词汇** | 只有两个命令：`Goto`（重新激活某节点，实现循环/回边，ReAct 就是靠它循环）、`Send`（运行时实例化模板节点，做 fan-out）。刻意**没有** Update 命令——写数据是 `StateDelta` 的事，"数据归数据，控制归控制" |
| **`channels.go`** | 定义**状态如何合并**（reducer） | 一个波内多个节点并发写同一个 key，不能简单覆盖。每个状态通道声明一个 `Reducer(old,new)->merged`：`lastReduce`（覆盖）、`appendReduce`（追加，如消息历史）、`addReduce`（累加，如成本）、`mergeReduce`（合并 map）。reducer 必须纯函数，这也是状态能被重放重建的前提 |

### 第 2 层：静态蓝图

| 文件 | 一句话作用 | 关键内容 |
|---|---|---|
| **`graph.go`** | 描述"要执行什么"的**静态图纸** | `Node`、`Edge`（`Source`/`Target`/`When` 条件谓词）、`RetryPolicy`（步骤级重试策略）、`Plan`。蓝图不含任何单次运行的动态数据，一个 `Plan` 可被多个并发 `Run` 复用。`Ready()` 是这里唯一"看起来动态"的方法：根据当前 Run 状态算出下一批可运行节点，但它**纯读取、不修改任何东西** |

### 第 3 层：可执行体

| 文件 | 一句话作用 | 关键内容 |
|---|---|---|
| **`body.go`** | 定义"节点到底跑什么"的**统一接口** | 在 kernel 眼里只有一种可调度的东西：满足 `NodeBody` 的可执行体，接收 `input` + `NodeContext`，返回 `Outcome`。`Outcome` 正交拆成四部分：`Value`（给下游）、`StateDelta`（经 reducer 折叠）、`Control`（Goto/Send）、`Suspend`（请求在此挂起）。内置四种 body：普通函数、工具调用、模型调用、激活子计划——**多智能体无非是某个 body 递归运行另一张图** |

### 第 4 层：一次动态执行

| 文件 | 一句话作用 | 关键内容 |
|---|---|---|
| **`run.go`** | 表示"蓝图的一次执行"，是**动态状态机** | `NodeRuntimeState`（每个节点在本次运行里的 status/output/attempts）、`Interrupt`（挂起令牌）、`Run` 状态机、以及最关键的 `Apply` —— **账本状态变更的唯一入口**。状态转换只有单一入口 `transition`，非法跳转立即失败；`maxRunDepth = 8` 限制 Run 树深度 |
| **`eventlog.go`** | 定义**事实之源** `Event` | 三部分：`Event`（不可变、只追加、永不修改的"已发生的事实"）、`ApplyEvent`（把单个 Event 折叠进 shared state 的纯函数）、`EventLog`（存储接口 + 进程内默认实现）。事件种类如 `run_started` / `node_started` / `node_completed` / `node_failed` / `state_delta` / `artifact_written`。刻意不做快照——仅凭事件流即可重建每个 Run |

### 第 5 层：与外部世界的边界

| 文件 | 一句话作用 | 关键内容 |
|---|---|---|
| **`bus.go`** | 内核对外观察/裁决的**唯一接缝** | 两种用法：① 横切插件用回调——`Fire`（观察：可观测性/计量/审计，报错绝不影响主流程）、`Check`（裁决：审批/权限/预算关卡，任一否决即拦截，**默认 fail-closed**：checker 自己报错也算否决）；② 流式消费者用 `Subscribe`（有界队列 + 背压：`block` 让生产者等待，`drop` 丢帧计数） |
| **`ports.go`** | 内核与外部实现的**依赖倒置边界** | 内核只知道接口：`LlmPort`（`Chat(messages) -> LlmReply`）、`ToolPort`、激活端口等。它不知道 OpenAI、向量库，也不知道某工具是 HTTP 还是本地函数。组合根注入真实实现，测试注入脚本化假件，让整条流水线离线且确定 |

### 第 6 层：引擎

| 文件 | 一句话作用 | 关键内容 |
|---|---|---|
| **`scheduler.go`** | 内核的**唯一引擎**，把前面所有零件组装成波循环 | 核心循环：`ready = plan.Ready(run)` → 波内**并发**运行（有界信号量，节点只产出 Outcome、绝不直接改共享状态）→ 波末**屏障**统一 fold deltas / apply commands。三个关键性质：波是一致性边界、挂起即"放手"（park 时 fact 已持久化，resume 时带回填值重跑）、多 Agent 不加新引擎（`SubPlanBody` 递归跑子 Run）。每次账本提交由 `commitMu` 串行化，故 body 并发执行但状态提交确定。入口方法：`Run()` / `Resume()` |

### 旁支：投影与辅助

这几个文件都是"读取同一批事件流"的不同视角，可在读完主线后再看：

| 文件 | 一句话作用 | 关键内容 |
|---|---|---|
| **`blob.go`** | 大字节存储，让事件流保持小而统一 | agent 产出文件（报告/表格/图片）时，**字节进 `BlobStore`**，事件日志只记一条指针（filename/version/uri/mime/size）作为 `ARTIFACT_WRITTEN` 事件。所以制品（artifact）和 state、trace 一样，是事件流的投影 |
| **`trace.go`** | 把事件流投影成 Run 的**因果树** | 每个 Event 携带 `run_id` / 单调 `seq` / `parent_id`。`BuildTrace(events)` 折叠出嵌套的 `Span`（一个 Span = 一个 Run），`RenderTrace(events)` 输出缩进的可读轨迹。这是零依赖的可观测性核心 |
| **`replay.go`** | 从记录的 Event 流**反向重建** Run | `Replay(plan, events)`：它是同一批事实的**第二个读取者，绝不是第二个调度器**——没有 `Ready()`、没有 body、没有 LLM/tool，只是 `for ev: run.Apply(ev)`，与 live 调度走的是**同一个变更入口**，因此二者不可能漂移。崩溃恢复 `Scheduler.Resume` 正是靠它 |
| **`util.go`** | reducer 与 fold 共用的**小型转换辅助** | `isIntish` / `toInt` / `toFloat` / `asList` / `copyList` / `copyMap`。存在的原因：Go 静态类型仍要折叠经状态通道传输的动态 `any` 值，且 JSON 往返后 int 可能以 float64 到达 |

---

## 三、它们如何协作：一次执行的完整数据流

把上面的文件串起来，一次 `scheduler.Run(plan, task, input)` 发生的事：

```
1. graph.Plan.Ready(run)          ← graph.go    算出本波就绪节点
        │
2. 并发运行每个节点的 body        ← body.go     FnBody / ToolBody / LlmBody / SubPlanBody
        │  （LlmBody 经 ports.LlmPort 调模型；ToolBody 经 ports.ToolPort 调工具）
        │  （body 只返回 Outcome，绝不直接改共享状态）
        ▼
3. 波末屏障结算                    ← scheduler.go
        │  · Outcome.StateDelta 经 channels.go 的 reducer 折叠进 shared state
        │  · Outcome.Control(Goto/Send) 由 scheduler 改变下一波 ready 集合  ← command.go
        │  · Outcome.Suspend 则整波 park，等待 resume
        ▼
4. 每一步都提交为不可变事实        ← run.go 的 Run.Apply(ev) 是唯一变更入口
        │  · 写入 eventlog.go 的 EventLog（append-only）
        │  · 文件类产出：字节进 blob.go，日志只记指针事件
        │  · 经 bus.go 的 Fire 广播给观察者 / Check 关卡裁决
        ▼
5. 回到第 1 步，直到没有就绪节点或 Run 结束

─── 事后（读同一批事件流的三个视角）───
   replay.go  → 重建出完全一致的 Run（崩溃恢复 / 时间旅行）
   trace.go   → 投影成因果树（可观测性）
   blob.go    → 按指针取回文件字节（制品库）
```

理解这张图，就抓住了内核的骨架：**`scheduler` 驱动波循环，`run.Apply` 是唯一写入口，`eventlog` 存下所有事实，其余文件要么是投影（replay/trace/blob），要么是边界（bus/ports），要么是规则（types/command/channels/graph/body）。**

---

## 四、快速上手命令

```bash
cd prodagent/go

go build ./kernel/          # 编译
go vet ./kernel/            # 静态检查
go test ./kernel/           # 运行全部测试
go test ./kernel/ -race     # 竞态检测（内核大量用 goroutine，建议跑）
go test ./kernel/ -v -run 'TestScheduler'   # 只看某一类，边读代码边对照测试
```

学习建议：**读某个源文件时，同步打开它对应的 `*_test.go`**。测试用 `scriptedLlm` / `fakeRegistry` 等离线假件（见 `testsupport_test.go`）驱动内核，用例名和断言就是最好的"这个文件该怎么用"的示例。
