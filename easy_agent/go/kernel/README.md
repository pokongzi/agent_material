# kernel — 测试与验证指南

`prodagent/go/kernel` 是 Python 内核（`src/kernel`）的 Go 忠实移植：一个**教学级、零第三方依赖**的 agent 执行内核。本目录下的 12 个源文件实现机制，11 个测试文件（共 **66 个用例**）验证其行为与 Python 原版逐条对齐。

本 README 说明**如何编译、测试、验证**这个内核。

---

## 1. 环境要求

| 项 | 要求 |
|---|---|
| Go | **1.20+**（`go.mod` 声明 `go 1.20`） |
| 依赖 | **无第三方依赖**，仅 Go 标准库（由 `purity_test.go` 强制守护） |
| 模块 | `module prodagent`，内核包路径为 `prodagent/kernel` |

> 所有命令均在 **`prodagent/go/`** 目录下执行（即 `go.mod` 所在目录）。

---

## 2. 快速开始

```bash
cd prodagent/go

# 编译
go build ./kernel/

# 静态检查
go vet ./kernel/

# 运行全部测试
go test ./kernel/

# 运行全部测试 + 竞态检测（内核大量使用 goroutine，强烈建议）
go test ./kernel/ -race

# 查看覆盖率
go test ./kernel/ -cover
```

预期结果：

```
ok      prodagent/kernel        coverage: 81.0% of statements
```

`go vet` 无输出即为通过；`-race` 必须全绿（内核的波内并发、Bus 背压、子 Run 委派都在竞态检测下验证过）。

---

## 3. 常用验证命令

```bash
# 详细输出每个用例
go test ./kernel/ -v

# 只跑某一类测试（正则匹配用例名）
go test ./kernel/ -run 'TestArtifact' -v          # artifacts 相关
go test ./kernel/ -run 'TestSubrun|TestMutual' -v # 子 Run 委派相关
go test ./kernel/ -run 'TestResume' -v            # 中断/恢复相关

# 禁用缓存，强制重跑（验证确定性 / trace 顺序时很有用）
go test ./kernel/ -count=1

# 竞态 + 详细 + 禁缓存，交付前的完整校验
go test ./kernel/ -race -count=1 -v

# 生成覆盖率报告（HTML）
go test ./kernel/ -coverprofile=cover.out
go tool cover -html=cover.out
```

**验证确定性**：内核强调"同一事件流 → 同一状态"。反复运行不应有任何抖动：

```bash
go test ./kernel/ -count=20 -race
```

---

## 4. 测试文件地图

| 测试文件 | 用例数 | 验证的内核机制 | 对应 Python 测试 |
|---|---|---|---|
| `scheduler_test.go` | 15 | 波引擎核心：线性/菱形并发、条件分支、back-edge 循环、动态 fan-out/fan-in、join 策略（all/any/quorum）、停滞与空转、L1/L2 body、call_id 唯一性 | `test_graph_scheduler.py` + `test_call_ids.py` |
| `artifacts_test.go` | 11 | 制品：字节入 BlobStore / 指针事实入流、版本递增、dict→JSON、DI 自定义 store、删除后版本不覆写、跨节点读回、pin 版本、resume 后读回、fold 拒绝版本回退 | `test_artifacts.py` |
| `bus_test.go` | 7 | Bus 三协议：Fire（观察，错误吞掉）、Check（裁决，fail-closed）、Subscribe（有界队列 + block/drop 背压） | `test_bus.py` + `test_backpressure.py` |
| `replay_test.go` | 7 | 事件溯源重放：动态 fanout、goto 回边、未走分支被 skip、挂起、失败波、parked goto 输入、缺 run_started 报错 | `test_replay.py` |
| `interrupt_test.go` | 6 | 中断/恢复：挂起→resume、drifted plan 拒绝、同波双 park、跨调度器共享日志恢复、波中崩溃续跑、已完成 Run 拒绝 resume | `test_interrupt.py` |
| `wave_settlement_test.go` | 5 | 波结算律：深委派不自锁（concurrency=1）、失败波仍结算兄弟、失败压过同波 park 但 ask 入日志、Goto 载荷跨 park 存活、终态关闭事件流 | `test_wave_settlement.py` |
| `trace_test.go` | 4 | Trace 投影：父子 Run 树、render 缩进、单 Run 无 children、DurationMS | `test_trace.py` |
| `subrun_test.go` | 4 | 多智能体：子 Run 委派返回、嵌套 Run 树、互相委派深度熔断、蓝图名≠运行态 | `test_subrun.py` |
| `channels_test.go` | 4 | 状态通道 reducer：last/append/add/merge、ambiguous write 检测 | `test_channels.py` |
| `eventlog_test.go` | 2 | 事件日志：追加/读取、投影折叠 | `test_eventlog.py` |
| `purity_test.go` | 1 | 纯净性守卫：内核仅依赖标准库，任何第三方 import 立即 CI 变红 | `test_purity.py` |
| `testsupport_test.go` | 0（夹具） | 离线确定性端口：`scriptedLlm`、`fakeRegistry`，排序辅助 | — |

`testsupport_test.go` 不含用例，提供全部测试共用的**离线假端口**，让整条流水线无需真实模型/工具即可确定性运行。

---

## 5. 测试如何做到"离线且确定"

内核通过**端口（DIP）**与外界解耦，测试注入脚本化假实现：

- **`scriptedLlm`**（实现 `LlmPort`）：按预设脚本依次返回 `string` / `ToolCall` / `[]ToolCall` / `LlmReply`，并记录每次收到的 messages，token 计量固定为 6。脚本耗尽返回 `"(script exhausted)"`。
- **`fakeRegistry`**（实现 `ToolPort`）：`add(name, fn)` 注册工具；未知名或 `fn` 报错 → `ToolFailure`，否则 `ToolSuccess`。

因此测试**不触网、不依赖时钟、不依赖调度顺序**——断言的是事件流与投影，而非时序。

---

## 6. 被验证的核心不变量

测试覆盖的不只是"能跑通"，而是内核的**设计契约**：

1. **事件溯源**：Event 是唯一真相源；state / trace / artifacts 都是事件流的投影（`replay_test`、`trace_test`、`artifacts_test`）。
2. **单一变更入口**：`Run.Apply(ev)` 是账本状态变更的唯一路径，live 调度与 replay 共用同一函数，二者不可能漂移（`replay_test`、`interrupt_test`）。
3. **波屏障一致性**：节点并发只产出 Outcome，`state_delta` 在屏障处由 reducer 折叠；失败/park 也在屏障统一结算（`wave_settlement_test`、`channels_test`）。
4. **控制集中**：仅 Goto / Send 两个命令，节点永不直接改他节点状态（`scheduler_test`）。
5. **背压与 fail-closed**：block 策略把压力顶回生产者，drop 策略计数不阻塞；checker 自身 panic → 视为否决（`bus_test`）。
6. **结构守卫**：Run 树深度上限 8，互相委派被结构性熔断（`subrun_test`）。
7. **纯净性**：内核零第三方依赖，由 `purity_test` 用 `go/parser` 解析 import 强制守护。

---

## 7. 与 Python 原版的有意差异

以下是 Python→Go 的**合理语言映射**，非缺陷，测试已相应适配：

| 维度 | Python | Go |
|---|---|---|
| 错误串格式 | `f"{type}: {msg}"`（如 `RuntimeError: boom`） | `err.Error()`（如 `boom`） |
| 未知节点事实 | `Apply` 抛 `KeyError` | `requireNode` 返回 error（更可捕获，语义等价） |
| 重试条件 | 异常类型元组 / 谓词 | `func(error) bool`（nil = 全重试） |
| FnBody 参数 | 按参数个数自适应 `f(x)`/`f(x,ctx)` | 统一 `Fn(input, ctx)` |
| 外部取消 | `asyncio.CancelledError` 专门传播 | 同步 body 不可中途取消；超时改为弃用 goroutine（`test_external_cancellation` 按语言模型跳过） |
| Bus.Fire 投递 | `asyncio.gather` 并发投递 | 按注册序顺序投递（观察者相互独立，等价） |

**未移植项**（属 backend/runtime 层，不在内核范围）：`LocalBlobStore`、`FileEventLog`、依赖 react runtime 的用例。相关 Python 测试中对应部分已在 Go 侧显式跳过。

---

## 8. 交付前检查清单

```bash
cd prodagent/go
go build ./kernel/                 # 编译通过
go vet ./kernel/                   # 静态检查无告警
go test ./kernel/ -race -count=1   # 全 66 用例绿 + 无数据竞态
go test ./kernel/ -cover           # 覆盖率 ≥ 80%
```

四项全绿即视为内核验证通过。
