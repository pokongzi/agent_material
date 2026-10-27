# 阿里云大模型ACP教程

🇺🇸 [English](./README-en.md) | 🇨🇳 简体中文

<img src="https://gw.alicdn.com/imgextra/i1/O1CN01xol8Y21oUw1VeGgbZ_!!6000000005229-0-tps-1096-569.jpg" alt="main" width="800px">

## 🎈 欢迎新同学

欢迎来到阿里云大模型ACP高级工程师认证课程，这是阿里云大模型认证的进阶篇。在开始课程之前，先来了解阿里云大模型认证的体系架构，方便你选择适合自己定位的课程。
阿里云大模型认证体系架构：

<img src="https://gw.alicdn.com/imgextra/i1/O1CN01MC00f61wMhpgctA1q_!!6000000006294-0-tps-1445-478.jpg" alt="我的notebook" width="800px">

> 如果你尚不具备编程基础，或者想从零开始了解大模型，请跳转:point_right:[阿里云大模型ACA工程师认证课程](https://edu.aliyun.com/course/3126500)

## [最近更新](./release_notes.md)
- [2026.06.30] V2.4.3 新增 Harness Engineering 与 Loop Engineering 课程
- [2026.04.30] V2.4.2 微调改为蒸馏
- [2026.04.10] V2.4.0 Qwen Code 实践课程
- [2026.03.27] V2.3.0 新增 Agent Skills 章节
- [2025.11.14] V2.2.0 重构 Agent 章节
- [2025.07.28] V2.1.0 引入 Meta Prompting
- [2025.07.24] V2.0.9 引入上下文工程框架
- [2025.06.27] V2.0.8 更新大模型应用安全合规内容
- [2025.06.13] V2.0.7 更新2.4节RAG自动化评测内容
- 详情请见[Release Notes](./release_notes.md)
  
## 🪶  课程定位

了解课程定位会帮助你更好地规划学习路径，确保课程内容与个人目标相匹配，从而提升学习效率和成果。通过学习阿里云大模型高级工程师ACP认证课程，你将
- 掌握以下知识与技能：
    - 大模型提示词技巧
    - 检索增强和微调的原理和流程
    - LangChain、Llama-Index等大模型开发组件的使用方法
    - 工程化评测的概念与方法
    - 大模型的规范和安全性
- 有能力完成以下任务：
    - 使用阿里云百炼平台构建大模型应用(开发、测评、部署、发布)
    - 使用提示词策略、检索增强、微调技术优化大模型回答质量
    - 使用Multi-Agent进行文本、图像、视频等多模态内容生产
    - 能够针对复杂业务场景设计并实施大模型驱动的解决方案
- 胜任以下岗位：
    - 大模型解决方案高级工程师
    - 大模型应用开发高级工程师


## 📙 课程列表

本课程面向具备编程基础的生成式人工智能技术爱好者和应用开发者，不仅传授理论知识，更聚焦于实战落地。我们将围绕一个完整项目——**新人答疑机器人**，系统化拆解从业务需求分析、场景适配、模型选型、Prompt 工程、应用开发到最终部署运维的全链路闭环。旨在培养学员针对复杂业务场景，独立设计并实施大模型驱动解决方案的能力，让你真正掌握从“想法”到“落地”完整路径。

<table>
<thead>
 <tr>
    <td style="background-color:#f2f2f2; font-weight:bold; padding:10px; border: 1px solid #ddd;">章节</td>
    <td style="background-color:#f2f2f2; font-weight:bold; padding:10px; border: 1px solid #ddd;">课时</td>
    <td style="background-color:#f2f2f2; font-weight:bold; padding:10px; border: 1px solid #ddd;">标题</td>
 </tr>
 </thead>
 <tbody>
  <tr>
    <td rowspan="1" style="background-color:#f9f9f9; padding:10px; border: 1px solid #ddd; vertical-align:top;">C1 课程准备</td>
    <td style="padding:10px; border: 1px solid #ddd;">1.1</td>
    <td style="padding:10px; border: 1px solid #ddd;">配置 AI 开发环境</td>
  </tr>
  <tr>
    <td rowspan="6" style="background-color:#f9f9f9; padding:10px; border: 1px solid #ddd; vertical-align:top;">C2 构造问答系统</td>
    <td style="padding:10px; border: 1px solid #ddd;">2.0</td>
    <td style="padding:10px; border: 1px solid #ddd;">项目背景</td>
  </tr>
  <tr>
    <td style="padding:10px; border: 1px solid #ddd;">2.1</td>
    <td style="padding:10px; border: 1px solid #ddd;">用大模型构建新人答疑机器人</td>
  </tr>
  <tr>
    <td style="padding:10px; border: 1px solid #ddd;">2.2</td>
    <td style="padding:10px; border: 1px solid #ddd;">扩展答疑机器人的知识范围</td>
  </tr>
  <tr>
    <td style="padding:10px; border: 1px solid #ddd;">2.3</td>
    <td style="padding:10px; border: 1px solid #ddd;">优化提示词改善回答质量</td>
  </tr>
  <tr>
    <td style="padding:10px; border: 1px solid #ddd;">2.4</td>
    <td style="padding:10px; border: 1px solid #ddd;">自动化评测答疑机器人的表现</td>
  </tr>
  <tr>
    <td style="padding:10px; border: 1px solid #ddd;">2.5</td>
    <td style="padding:10px; border: 1px solid #ddd;">优化 RAG 应用提升问答准确度</td>
  </tr>
  <tr>
    <td rowspan="9" style="background-color:#f9f9f9; padding:10px; border: 1px solid #ddd; vertical-align:top;">C3 构建 Agent 系统</td>
    <td style="padding:10px; border: 1px solid #ddd;">3.0</td>
    <td style="padding:10px; border: 1px solid #ddd;">从回答问题到解决问题</td>
  </tr>
  <tr>
    <td style="padding:10px; border: 1px solid #ddd;">3.1</td>
    <td style="padding:10px; border: 1px solid #ddd;">Agent 基础与工具调用</td>
  </tr>
  <tr>
    <td style="padding:10px; border: 1px solid #ddd;">3.2</td>
    <td style="padding:10px; border: 1px solid #ddd;">让 Agent 学会规划与执行</td>
  </tr>
  <tr>
    <td style="padding:10px; border: 1px solid #ddd;">3.3</td>
    <td style="padding:10px; border: 1px solid #ddd;">用多 Agent 实现团队协作</td>
  </tr>
  <tr>
    <td style="padding:10px; border: 1px solid #ddd;">3.4</td>
    <td style="padding:10px; border: 1px solid #ddd;">用 Memory 让 Agent 积累经验</td>
  </tr>
  <tr>
    <td style="padding:10px; border: 1px solid #ddd;">3.5</td>
    <td style="padding:10px; border: 1px solid #ddd;">用 Skill 将能力固化为可复用流程</td>
  </tr>
  <tr>
    <td style="padding:10px; border: 1px solid #ddd;">3.6</td>
    <td style="padding:10px; border: 1px solid #ddd;">用评测驱动 Agent 开发</td>
  </tr>
  <tr>
    <td style="padding:10px; border: 1px solid #ddd;">3.7</td>
    <td style="padding:10px; border: 1px solid #ddd;">Qwen Code 实践</td>
  </tr>
  <tr>
    <td style="padding:10px; border: 1px solid #ddd;">3.8</td>
    <td style="padding:10px; border: 1px solid #ddd;">用 Harness Engineering 和 Loop Engineering 打造可上线的业务能力</td>
  </tr>
  <tr>
    <td rowspan="5" style="background-color:#f9f9f9; padding:10px; border: 1px solid #ddd; vertical-align:top;">C4 交付上线</td>
    <td style="padding:10px; border: 1px solid #ddd;">4.0</td>
    <td style="padding:10px; border: 1px solid #ddd;">走向生产环境</td>
  </tr>
  <tr>
    <td style="padding:10px; border: 1px solid #ddd;">4.1</td>
    <td style="padding:10px; border: 1px solid #ddd;">用蒸馏让小模型掌握专业能力</td>
  </tr>
  <tr>
    <td style="padding:10px; border: 1px solid #ddd;">4.2</td>
    <td style="padding:10px; border: 1px solid #ddd;">部署模型</td>
  </tr>
  <tr>
    <td style="padding:10px; border: 1px solid #ddd;">4.3</td>
    <td style="padding:10px; border: 1px solid #ddd;">大模型应用生产实践</td>
  </tr>
  <tr>
    <td style="padding:10px; border: 1px solid #ddd;">4.4</td>
    <td style="padding:10px; border: 1px solid #ddd;">大模型应用安全合规</td>
  </tr>
  <tr>
    <td rowspan="2" style="background-color:#f9f9f9; padding:10px; border: 1px solid #ddd; vertical-align:top;">C5 总结与展望</td>
    <td style="padding:10px; border: 1px solid #ddd;">5.1</td>
    <td style="padding:10px; border: 1px solid #ddd;">培养品味用 AI 为业务提效</td>
  </tr>
  <tr>
    <td style="padding:10px; border: 1px solid #ddd;">5.2</td>
    <td style="padding:10px; border: 1px solid #ddd;">总结与展望</td>
  </tr>
  </tbody>
</table>



## 💯 考试大纲

带着目的学习可以提升学习效率。在开始课程之前，请了解大模型ACP认证的考试大纲，将更有利于你的课程学习。
### 🌟 考试知识点分布


<table>
<thead>
 <tr>
    <td style="background-color:#f2f2f2; font-weight:bold; padding:10px; border: 1px solid #ddd;">考核知识点</td>
    <td style="background-color:#f2f2f2; font-weight:bold; padding:10px; border: 1px solid #ddd;">试题比例</td>
 </tr>
 </thead>
 <tbody>
  <tr>
    <td style="padding:10px; border: 1px solid #ddd;">大模型应用开发</td>
    <td style="padding:10px; border: 1px solid #ddd;">17%</td>
  </tr>
  <tr>
    <td style="padding:10px; border: 1px solid #ddd;">大模型提示词工程</td>
    <td style="padding:10px; border: 1px solid #ddd;">15%</td>
  </tr>
  <tr>
    <td style="padding:10px; border: 1px solid #ddd;">大模型检索增强</td>
    <td style="padding:10px; border: 1px solid #ddd;">20%</td>
  </tr>
  <tr>
    <td style="padding:10px; border: 1px solid #ddd;">大模型微调</td>
    <td style="padding:10px; border: 1px solid #ddd;">16%</td>
  </tr>
  <tr>
    <td style="padding:10px; border: 1px solid #ddd;">多Agent及多模态应用</td>
    <td style="padding:10px; border: 1px solid #ddd;">16%</td>
  </tr>
  <tr>
    <td style="padding:10px; border: 1px solid #ddd;">生产环境应用实践</td>
    <td style="padding:10px; border: 1px solid #ddd;">16%</td>
  </tr>

  </tbody>
</table>

### 🌟 考试大纲


#### 1. 大模型应用开发

**主要考察内容**：通过 API 调用构建大模型应用的基础能力，包括理解大模型工作原理、核心参数调优、对话管理和成本意识。

**核心知识点**：
- 大模型基本工作方式
- 核心参数与输出控制
- 多轮对话与流式输出
- 上下文窗口与幻觉缓解
- API 安全管理与 Token 计费

#### 2. 大模型提示词工程

**主要考察内容**：通过提示词设计提升大模型输出质量的能力，包括系统提示词设计、进阶提示技巧和场景化应用。

**核心知识点**：
- 系统提示词设计
- 上下文工程
- 思维链、Meta Prompting、Few-shot
- 结构化输出与格式控制
- 提示词的适用边界与局限

#### 3. 大模型检索增强

**主要考察内容**：RAG 检索增强生成的全链路设计与优化能力，包括索引构建、检索策略、质量诊断和知识库运维。

**核心知识点**：
- RAG 全链路：索引、检索、增强、生成
- 文本切分策略与检索质量
- 检索效果诊断与重排序
- 多轮对话场景下的检索适配
- 知识库运维：更新、权限、版本管理

#### 4. 大模型微调

**主要考察内容**：微调与蒸馏的适用判断、训练基础概念和实操能力，包括何时该微调、怎么训练、怎么评估效果。

**核心知识点**：
- 微调适用场景判断
- 训练基础：梯度下降、损失函数、过拟合与欠拟合
- LoRA 微调与训练调参
- 蒸馏路径：数据合成、知识蒸馏、推理压缩
- 训练数据合成与成本收益分析

#### 5. 智能体应用开发

**主要考察内容**：构建 Agent 系统的核心能力，包括工具调用、任务规划、多 Agent 协作、记忆与能力固化，以及多模态应用的架构设计。

**核心知识点**：
- Function Calling 机制与 JSON Schema 设计
- ReAct 循环与 MCP 协议
- Plan & Execute、任务依赖与固定流程 vs 自主规划的选型
- SubAgent 分层协作模式
- 四类记忆体系与 Skill 能力固化
- Harness Engineering 核心闭环
- 多模态 API 调用与端到端应用架构

#### 6. 生产环境应用实践

**主要考察内容**：将大模型应用交付到生产环境的工程能力，包括部署与成本控制、安全防护与合规、评测驱动与业务判断。

**核心知识点**：
- 部署方案选型
- 推理成本控制
- 生产稳定性保障
- 安全体系：内容安全、注入防护、纵深防御、合规备案
- 评测驱动开发
- RIDE 方法论与 AI 应用选点

本考纲旨在为考生提供考试内容的普遍方向，考试范围不仅限于文中提及的部分，可能还包括其他相关未列明的内容。

## 🛠️ 教程及代码

本教程假设你已经初步了解并使用过 python、git，因此不会涉及如何安装 python、pip、git 等基础工具。你可以通过脚本“自动安装”或者“手动下载代码”在你的系统上安装课程文件。

### 1.自动安装
如果你对Linux环境熟悉，你可以体验使用脚本自动完成课程文件下载和依赖项安装。

在 DSW 的 Linux 环境，或启动 MAC 的命令行界面，点击下载[aliyun_llm_acp_install脚本](https://developer-labfileapp.oss-cn-hangzhou.aliyuncs.com/ACP/aliyun_llm_acp_install.sh)，或者输入如下命令，即可完成项目安装。

```bash
wget https://developer-labfileapp.oss-cn-hangzhou.aliyuncs.com/ACP/aliyun_llm_acp_install.sh
/bin/bash aliyun_llm_acp_install.sh
```
详情可参考[《1_0_计算环境准备》](./大模型ACP认证教程/p1_课程准备/1_0_计算环境准备.ipynb)

顺利执行上述命令后，你可以使用你的百炼API-KEY，开始你的学习。

### 2.手动下载代码

本教程github地址如下：
```
git clone https://github.com/AlibabaCloudDocs/aliyun_acp_learning.git
cd aliyun_acp_learning
pip install -r requirements.txt
```

如果遇到网络问题，GitHub无法使用，可以选择从atomgit来获取代码库

#
```
git clone https://atomgit.com/alibabaclouddocs/aliyun_acp_learning.git
cd aliyun_acp_learning
pip install -r requirements.txt
```



## 📌 问题反馈

如果你在学习过程中遇到任何问题，欢迎你[通过问卷提交评价和反馈学习体验](https://survey.aliyun.com/apps/zhiliao/Mo5O9vuie)。
你的批评和鼓励都是我们前进的动力！

---

如果这个项目对你有帮助，请给个 Star ⭐️ 吧！
