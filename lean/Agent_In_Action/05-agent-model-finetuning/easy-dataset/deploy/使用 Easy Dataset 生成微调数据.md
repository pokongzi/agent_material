# 使用 Easy Dataset 生成微调数据

[Easy Dataset](https://github.com/ConardLi/easy-dataset) 是一个专为创建大型语言模型（LLM）微调数据集而设计的应用程序。它提供了直观的界面，用于上传特定领域的文件，智能分割内容，生成问题，并为模型微调生成高质量的训练数据。支持使用 OpenAI、DeepSeek、火山引擎等大模型 API 和 Ollama 本地模型调用。

## 📦 安装 Easy Dataset

```bash
# 进入项目目录
cd 05-agent-model-finetuning/easy-dataset/deploy

# 后台运行（推荐）
docker compose up -d
```

**✅ 启动成功标志**：

```bash
# docker compose logs -f --tail=200 easy-dataset 
easy-dataset  | \033[0;32m=== Easy Dataset Database Initialization ===\033[0m
easy-dataset  | \033[1;33mCreating prisma directory...\033[0m
easy-dataset  | \033[1;33mDatabase file not found at: /app/prisma/db.sqlite\033[0m
easy-dataset  | \033[0;32mPrisma directory is empty. Initializing from template...\033[0m
easy-dataset  | \033[0;32mDatabase initialized from template!\033[0m
easy-dataset  | \033[0;32m=== Database Ready! Starting application... ===\033[0m
easy-dataset  | 
easy-dataset  | 
easy-dataset  | > easy-dataset@1.6.1 start /app
easy-dataset  | > next start -p 1717
easy-dataset  | 
easy-dataset  |   ▲ Next.js 14.2.29
easy-dataset  |   - Local:        http://localhost:1717
easy-dataset  | 
easy-dataset  |  ? Starting...
easy-dataset  |  ? Ready in 492ms
```

## 📊 示例数据下载

本教程提供了专门针对原始医疗数据文本：

```bash
# 医疗数据文本
05-agent-model-finetuning/easy-dataset/dataset/MedicalData-2025
```

## 🎯 微调数据生成

### 第一步：创建项目

> 在浏览器进入 Easy Dataset 主页后，点击**创建项目**

1. **项目初始化**

   ```
   浏览器访问：http://localhost:1717
   点击"创建项目" → 输入项目名称："Medical-Dataset"
   ```

2. **模型配置**

   > 项目创建后会跳转到**项目设置**页面，打开**模型配置**，选择数据生成时需要调用的大模型 API 接口

   ![模型配置](https://cdn.jsdelivr.net/gh/Fly0905/note-picture@main/imag/202512021419876.png)

   > 这里以 DeepSeek 模型为例，修改模型**提供商**和**模型名称**，填写 **API 密钥**，点击**保存**后将数据保存到本地，在右上角选择配置好的模型

   ![](https://cdn.jsdelivr.net/gh/Fly0905/note-picture@main/imag/202512021419122.png)

   - **提供商**：DeepSeek

   - **模型名称**：[deepseek-chat](https://api-docs.deepseek.com/zh-cn/)

   - **API密钥**：[deepseek API KEY](https://platform.deepseek.com/api_keys)

   - **请求配置**：

     ```json
     {
       "temperature": 0.7,
       "max_tokens": 8192
     }
     ```
     
     ![模型测试](https://cdn.jsdelivr.net/gh/Fly0905/note-picture@main/imag/202512021423212.png)

3. **任务配置**

   > 打开**任务配置**页面，设置（初次接触，可使用默认的配置）

   ![任务配置](https://cdn.jsdelivr.net/gh/Fly0905/note-picture@main/imag/202512021424524.png)

   ```bash
   文本分割设置：
   - 最小长度：2500字符
   - 最大长度：4000字符（保证上下文完整性）
   
   问题生成设置：
   - 生成密度：每240个字符1个问题（平衡质量与数量）
   ```

4. **提示词配置**

   > 初次接触，可使用默认的配置

   ![image-20251202142809400](https://cdn.jsdelivr.net/gh/Fly0905/note-picture@main/imag/202512021428857.png)

### 第二步：文献处理

1. **上传新文献**

   > 1. 打开**数据源-文献处理**页面，**点击右上角选择刚配置的模型**
   > 2. 选择并上传示例数据文件，选择文件后点击**上传并处理文件**
   > 3. 上传后会调用大模型解析文件内容并分块，耐心等待文件处理完成，示例数据通常需要 2 分钟左右

   ```bash
   同时上传多个文件：
   - 选择所有文件（txt + markdown格式）
   ```

   ![上传文件](https://cdn.jsdelivr.net/gh/Fly0905/note-picture@main/imag/202512021432514.png)

2. **生成微调数据**

   > 1. 待文件处理结束后，可以看到文本分割后的文本段，**选择全部文本段**，点击**批量生成问题**
   > 2. 点击后会调用大模型根据文本块来构建问题，耐心等待处理完成。视 API 速度，处理时间可能在 20-40 分钟不等
   > 3. 处理完成后，打开**问题管理**页面，选择全部问题，点击**批量构造数据集**，耐心等待数据生成。视 API 速度，处理时间可能在 20-40 分钟不等

   ![批量生成问题](https://cdn.jsdelivr.net/gh/Fly0905/note-picture@main/imag/202512021437749.png)

   ![image-20251202144150521](https://cdn.jsdelivr.net/gh/Fly0905/note-picture@main/imag/202512021441636.png)
   
   > 问题生成成功后，页面可以看到问题数量，问题详情
   
   ![image-20251202144503930](https://cdn.jsdelivr.net/gh/Fly0905/note-picture@main/imag/202512021445385.png)
   
   > 3. 问题生成成功后，打开**问题管理**页面，选择全部问题，点击**自动生成数据集-生成单轮对话数据集**，耐心等待数据生成。视 API 速度，处理时间可能在 20-40 分钟不等
   
   ![构造数据集](https://cdn.jsdelivr.net/gh/Fly0905/note-picture@main/imag/202512021450180.png)

### 第三步：数据集管理

> 1. 答案全部生成结束后，打开**数据集管理**页面，点击**导出数据集**
> 2. 在导出配置中选择**在** **LLaMA Factory** **中使用**，点击**生成 LLaMA Factory** **配置**，即可在对应文件夹下生成配置文件，点击**复制**按钮可以将配置路径路径复制到粘贴板。
> 3. 在配置文件路径对应的文件夹中可以看到生成的数据文件，其中主要关注以下三个文件
>    1. dataset_info.json：LLaMA Factory 所需的数据集配置文件
>    2. alpaca.json：以 Alpaca 格式组织的数据集文件
>    3. sharegpt.json：以 Sharegpt 格式组织的数据集文件
>
> 其中 alpaca 和 sharegpt 格式均可以用来微调，两个文件内容相同。

![导出数据集](https://cdn.jsdelivr.net/gh/Fly0905/note-picture@main/imag/202512021454262.png)

> 在配置文件路径对应的文件夹中可以看到生成的数据文件，其中
>
> - `/app/local-db/sPuvMnx0E0i1/` 是Easy Dataset容器内的路径。需要切换到本地路径下载
> - `/app/local-db/sPuvMnx0E0i1/` ，默认对应`./local-db/sPuvMnx0E0i1/`。其中`.`表示你部署`Easy Dataset`服务的`docker-compose.yml`路径。

![image-20251202145935041](https://cdn.jsdelivr.net/gh/Fly0905/note-picture@main/imag/202512021459341.png)

![后台服务日志](https://cdn.jsdelivr.net/gh/Fly0905/note-picture@main/imag/202512021500818.png)

**文件结构验证**

```bash
(agent101) root@fly:/Agent101/code/Agent_In_Action/05-agent-model-finetuning/easy-dataset/deploy/local-db/sPuvMnx0E0i1# ll
total 264
drwxr-xr-x 4 root root   4096 Dec  2 14:54 ./
drwxr-xr-x 3 root root   4096 Dec  2 14:16 ../
-rw-r--r-- 1 root root  51040 Dec  2 14:54 alpaca.json
-rw-r--r-- 1 root root    927 Dec  2 14:54 dataset_info.json
drwxr-xr-x 2 root root   4096 Dec  2 14:34 files/
-rw-r--r-- 1 root root 127123 Dec  2 14:54 multilingual-thinking.json
-rw-r--r-- 1 root root  60836 Dec  2 14:54 sharegpt.json
-rw-r--r-- 1 root root    350 Dec  2 14:26 task-config.json
drwxr-xr-x 2 root root   4096 Dec  2 14:34 toc/


# dataset_info.json  - LLaMA Factory配置
# alpaca.json       - Alpaca格式数据
# sharegpt.json     - ShareGPT格式数据
# 其中 alpaca 和 sharegpt 格式均可以用来微调，两个文件内容相同。
```

#### ✅ easy-dataset 生成文件总览表

| 文件 / 目录                    | 内容说明                                               | 是否用于微调            | 备注                                      |
| ------------------------------ | ------------------------------------------------------ | ----------------------- | ----------------------------------------- |
| **dataset_info.json**          | 数据集元信息，包含样本数量、字段结构、构建参数等       | ✔️（LLaMA Factory 必须） | LLaMA Factory 会根据此文件自动识别数据集  |
| **alpaca.json**                | Alpaca 指令格式（instruction / input / output）        | ✔️                       | 与 sharegpt.json 内容相同，仅格式结构不同 |
| **sharegpt.json**              | ShareGPT 多轮对话格式（conversations 列表）            | ✔️                       | 与 alpaca.json 内容相同，可二选一         |
| **multilingual-thinking.json** | 多语言推理（CoT）数据，含 reasoning + answer           | 取决于训练任务          | 用于推理增强、多语言任务                  |
| **files/**                     | 构建过程中的中间文件、原始 API 输出、chunk 文件        | ✖️                       | 便于调试数据生成过程                      |
| **toc/**                       | 数据分块索引（Table of Contents），记录分片与追踪信息  | ✖️                       | 用于大规模数据集管理、恢复、合并          |
| **task-config.json**           | easy-dataset 的任务入口配置文件（pipeline、source 等） | ✖️（但会影响输出）       | 本次数据构建所有内容的来源                |
| **目录本身（dataset root）**   | 存放所有生成的数据集文件                               | —                       | 每次执行会生成新的时间戳目录              |

------

#### ✅ 重点文件

以下三个文件是实际微调用得最多的：

| 文件名                | 用途说明                                           |
| --------------------- | -------------------------------------------------- |
| **dataset_info.json** | LLaMA Factory 的数据集描述文件，必须存在           |
| **alpaca.json**       | 完整的 Alpaca 指令格式数据，可直接用于 SFT 微调    |
| **sharegpt.json**     | 完整的 ShareGPT 对话格式数据，可直接用于 Chat 微调 |

📌 **注意**：
 `alpaca.json` 和 `sharegpt.json` 内容相同，仅结构不同，你可任选其一用于 SFT / Chat 微调。

![image-20251202152122488](https://cdn.jsdelivr.net/gh/Fly0905/note-picture@main/imag/202512021521024.png)

------

