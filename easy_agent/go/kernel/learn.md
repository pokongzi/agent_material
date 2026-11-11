第 6 步：eventlog.go（~130 行，短小但是全套设计的基石）
重点看：Event 结构（Seq / RunID / Kind / Data / ParentID / Ts）、14 个 EvXxx 常量、ApplyEvent（只处理 EvStateDelta）、FoldEvents、EventLog 接口 + InMemoryEventLog 默认实现；
心智锚点："state = fold(events)" —— 这条一句话讲完的等式，是后面所有代码的动机；
配套测试：eventlog_test.go（能看到一条 Run 完整事件流的形态）。
🔵 第 7 步：run.go（~455 行，核心中的核心）
重点看：
Run 结构（EventSeq / Shared / NodeStates / Instances / Deliveries / ResumeValues / State …）——把 eventlog 里的 fact 投影成运行时对象；
Run.Apply(ev)：唯一的状态变更入口，switch ev.Kind 逐类事实处理；live 与 replay 共用；
Run.Resume(values) / Rearm / MarkRunning/Completed/Failed/Skipped 这套生命周期函数；
WaveDelta（用到 Channel.Empty 起步做波内聚合）；
IsTerminal / IsCompleted / IsPending / IsInstance 这些状态查询——graph.Ready 全靠它们。
心智锚点：Apply 是"账本 → 投影"的翻译器；读透它就理解了内核为什么"没有快照也能恢复"。
配套测试：interrupt_test.go（Park/Resume 生命周期）、artifacts_test.go（Blob + facts 联动）。
🟢 第 8 步：ports.go（~57 行，最短但最关键）
重点看：三个 DIP 接口 LlmPort / ToolPort / SubagentPort 的签名与语义——Chat / Dispatch / Activate；
心智锚点："厂商 / DB / 远程实现都住在这里的对面"——内核只见到接口。这条边界一旦记住，读 scheduler 时不会被"llm/tools/subagent 从哪来"绊住。
🟢 第 9 步：blob.go（~226 行）
重点看：BlobStore 接口 + 内存实现 + SaveArtifact / LoadArtifact 与 EvArtifactWritten fact 的配合（"字节进 blob、指针进账本"）；
心智锚点：这是"大对象旁路"——账本只存指针、blob 存内容，跟 EventLog 是对称设计。
🟡 第 10 步：bus.go（~227 行，你已经在 Q&A 里啃过大半）
系统重读：把 Bus / Subscription / Observer / Checker / Fire / Check / Subscribe 的三种流量（观察 / 裁决 / 订阅）在完整心智模型下再走一遍——之前是零散看的，这次串起来；
心智锚点："内核与外界的唯一接缝"——Scheduler.commit 的第 ③ 步就靠它。
配套测试：bus_test.go（block/drop 背压、Close 注销）。
🔴 第 11 步：scheduler.go（~717 行，最大最重，压轴）
前置：必须先啃完 eventlog + run + ports + blob + bus，否则读它处处是黑盒；
重点看（按你已建立的坐标系）：
Scheduler 结构 + commitMu + NewScheduler 默认端口注入；
commit（L648-664）：log → apply → fire 三步曲——所有 fact 从这里诞生；
drive（L192+）：主循环——首次进入落 run_started + Seed、波循环（nextReady → arm 单线程 → execNode 并发 → 屏障 WaveWrites/WaveDelta → settle）；
nextReady（L358-365）：两阶段收敛（先严格 Ready(false)、sweep、再宽松 Ready(true)）；
execNode / runBody：body 调用点 + NodeContext 组装（record/facts/ResumeValue 就是在这里注入的）；
settle：终态 fact（run_completed / run_failed）+ finalOutput 组装；
Resume：跨进程崩溃恢复入口，先 replay 再 drive；
InProcessActivator.Activate（L678-716）：Spawn 的默认实现——ChildOf → recordDelegation → drive → ChildResult；
invalidControl / applyControl：Goto/Send 的落地。
心智锚点："编排层"——它自己不定义新语义，只是把前面读过的所有零件按纪律组装。
配套测试：scheduler_test.go、subrun_test.go、wave_settlement_test.go、replay_test.go——这四个测试串起来能验证你所有的心智模型。