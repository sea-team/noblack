# noblack

一个使用 Go 编写的轻量级文本关键词匹配服务。项目基于 Aho-Corasick 自动机，提供关键词扫描、分类标签、备注信息、在线词库管理、运行统计与热更新能力，并内置了可直接使用的 Web 控制台。

适合用于内容分类、文本标注、规则路由、合规提示、客服质检等需要按词库快速识别文本片段的场景。

## 功能概览

- **高效匹配**：使用 Aho-Corasick 自动机一次扫描多个关键词。
- **Unicode 支持**：按 `rune` 处理文本，支持中文、英文、Emoji 等内容，并返回准确的字符区间。
- **灵活元数据**：每个词条可配置多个分类标签与多条备注。
- **批量词条**：一个配置项可通过中英文逗号声明多个关键词，共享同一组标签和备注。
- **在线管理**：通过 Web 控制台或 HTTP API 查询、新增、修改和删除词条。
- **动态更新**：支持监听词库文件自动重载，也可通过 API 手动触发重载。
- **并发友好**：新自动机在后台构建完成后通过 `atomic.Value` 发布，查询路径无需等待重建过程。
- **运行统计**：记录请求量、匹配量和高频命中项，可选定期持久化。
- **访问控制**：可为词库写操作配置访问令牌。
- **便于部署**：支持本地运行、Docker 和 Docker Compose，无需单独部署前端。

完整接口定义及响应字段说明请参阅 [API.md](./API.md)。

## 快速开始

### 克隆仓库（重要：模型权重走 Git LFS）

`models/**/model.safetensors` 由 Git LFS 管理。**直接 `git clone` 拿到的是 132 字节的指针文件，不是真实权重**，此时模型服务无法启动、发布包也构建不出来（`release.py` 会主动拒绝 LFS 指针）。

```bash
# 先装 git-lfs：Debian/Ubuntu 用 apt install git-lfs，macOS 用 brew install git-lfs
git lfs install
git clone <仓库地址>
cd noblack
git lfs pull          # 拉取约 800MB 的模型权重
```

已经克隆过但忘了装 LFS，补拉即可：

```bash
git lfs install && git lfs pull
```

检查是否拉取成功——真实权重是几百 MB，指针只有 132 字节：

```bash
ls -la models/lite-production-v1/model.safetensors
```

> 只做词库检测、不启用 AI 模型时可以跳过这一步，用 `-model-service-url ""` 启动即可。

### 本地运行

环境要求：Go 1.21 或更高版本。

```bash
go mod download
go run ./cmd/server
```

服务默认监听 `:8080`，词库默认读取当前目录下的 `data/words.json`。

启动后可访问：

- Web 控制台：`http://localhost:8080`
- 健康检查：`http://localhost:8080/health`

常用启动方式：

```bash
# 指定监听地址和词库文件
go run ./cmd/server -addr :8080 -words ./data/words.json

# 启用英文大小写忽略匹配
go run ./cmd/server -ci

# 持久化运行统计
go run ./cmd/server -stats-file ./stats.json

# 为词库写操作启用访问令牌
go run ./cmd/server -token "your-secret-token"

# 关闭文件监听，仅通过 API 手动重载
go run ./cmd/server -watch=false
```

### Docker Compose

```bash
docker compose up -d --build
```

默认配置会将宿主机的 `./data` 挂载到容器 `/data`：

- `./data/words.json`：词库文件
- `./data/stats.json`：统计数据

容器和服务进程均以 root 身份运行，避免宿主机绑定挂载目录的 UID/GID 与容器用户不一致，导致无法创建临时文件、更新词库或持久化统计。

首次启动且 `./data/words.json` 不存在时，容器会自动复制一份默认词库。更新配置后需要重新构建并创建容器：

```bash
docker compose down
docker compose up -d --build --force-recreate
```

### Docker

```bash
docker build -t noblack:latest .

mkdir -p ./data
docker run -d \
  --name noblack \
  -p 8080:8080 \
  -v "$(pwd)/data:/data" \
  noblack:latest
```

## 词库格式

默认词库采用 JSON 格式：

```json
{
  "words": [
    {
      "word": "售后服务",
      "levels": ["customer-service"],
      "remarks": ["转交售后流程"]
    },
    {
      "word": "退款,退货",
      "levels": ["after-sales", "priority"],
      "remarks": ["需要人工复核"]
    },
    {
      "word": "Example",
      "level": "demo",
      "remarks": "英文示例"
    }
  ]
}
```

字段说明：

| 字段 | 类型 | 说明 |
|------|------|------|
| `word` | string | 待匹配的关键词；可用中文或英文逗号分隔多个词 |
| `levels` | string[] | 推荐写法，一个词条可配置多个分类标签 |
| `level` | string | 兼容单标签写法；与 `levels` 同时存在时优先使用 `levels` |
| `remarks` | string[] / string | 备注列表，也可写成逗号分隔的字符串 |

补充规则：

- 未提供 `level` 或 `levels` 时，使用 `-default-level` 指定的默认值。
- 顶层既可以使用 `{ "words": [...] }`，也可以直接使用数组 `[...]`。
- 逗号分隔的多个关键词会分别参与匹配和统计，但共享标签与备注。
- 保存词库时会清理空白项，并以规范 JSON 格式写回文件。

## HTTP API

主要接口如下：

| 分类 | 方法与路径 | 说明 |
|------|------------|------|
| 文本匹配 | `POST /check` | 扫描文本并返回命中位置及元数据 |
| 词库管理 | `GET /words` | 分页查询词条（支持关键词筛选） |
| 词库管理 | `POST /words` | 新增词条 |
| 词库管理 | `PUT /words/{word}` | 修改词条 |
| 词库管理 | `DELETE /words/{word}` | 删除词条 |
| 配置查询 | `GET /levels` | 获取当前词库中的全部标签 |
| 动态更新 | `POST /reload` | 从文件重新加载词库 |
| 运行统计 | `GET /stats` | 获取统计信息（高频词支持分页） |
| 运行统计 | `POST /stats/reset` | 重置统计信息 |
| 鉴权 | `GET /auth/status` | 查询是否启用写操作鉴权 |
| 鉴权 | `POST /auth/verify` | 校验访问令牌 |
| 运维 | `GET /health` | 健康检查 |

示例：

```bash
# 扫描文本
curl -X POST http://localhost:8080/check \
  -H 'Content-Type: application/json' \
  -d '{"text":"我需要申请退款并联系售后服务"}'

# 新增词条
curl -X POST http://localhost:8080/words \
  -H 'Content-Type: application/json' \
  -H 'X-Auth-Token: your-secret-token' \
  -d '{"word":"物流查询","levels":["customer-service"],"remarks":["转交物流流程"]}'

# 查看统计
curl "http://localhost:8080/stats?top=10"
```

写操作令牌同时支持以下两种请求头：

```text
X-Auth-Token: your-secret-token
Authorization: Bearer your-secret-token
```

更多请求、响应和错误码示例见 [API.md](./API.md)。

## 配置项

### 命令行参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-addr` | `:8080` | HTTP 监听地址 |
| `-words` | `./data/words.json` | 词库文件路径 |
| `-watch` | `true` | 是否监听词库文件并自动重载 |
| `-ci` | `false` | 是否忽略英文大小写 |
| `-default-level` | `Low` | 词条未配置标签时使用的默认值 |
| `-stats-file` | 空 | 统计持久化文件；为空时不持久化 |
| `-stats-flush-interval` | `30s` | 统计数据定期写入文件的间隔 |
| `-token` | 空 | 词库写操作令牌；为空时不启用鉴权 |
| `-normalize` | `true` | 归一化输入以对抗变体绕过（`炸.药` → `炸药`）；亦可用环境变量 `NB_NORMALIZE` |
| `-samples-file` | 空 | 语义样本库文件路径；留空则禁用；亦可用环境变量 `NB_SAMPLES` |
| `-sample-threshold` | `0.75` | 语义样本相似度阈值（0-1），越高越严格；亦可用环境变量 `NB_SAMPLE_THRESHOLD` |
| `-detect-mode` | `both` | 检测模式：`model_only` / `model_first` / `word_only` / `word_first` / `both`；亦可用环境变量 `NB_DETECT_MODE`；请求体 `mode` 可覆盖 |
| `-recall-on-miss` | `false` | 优先链路**未命中**时补跑另一条链路以提高召回；亦可用环境变量 `NB_RECALL_ON_MISS`；请求体 `recall_on_miss` 可覆盖 |

### 配置文件 config.env

程序启动时会自动查找并加载 `config.env`（依次尝试**可执行文件所在目录**和**当前工作目录**），因此直接运行 `noblack` / `noblack.exe` 也能读到配置，无需经过启动脚本。可用 `-config <路径>` 显式指定。

启动日志会明确打印配置来源，便于确认是否生效：

```
已加载配置文件: D:\...\config.env (生效 14 项)
未找到 config.env, 使用默认配置与命令行参数 (可用 -config 指定路径)
```

配置优先级由低到高：**config.env → 环境变量 → 命令行参数 → 请求体字段**（后者覆盖前者）。文件中只有 `NB_` 前缀的大写键会生效，与启动脚本的解析规则一致。

### 输入归一化（对抗变体绕过）

黑产常在敏感词中间插入字符来绕过检测——`炸.药`、`炸 药`、`炸_药`、`炸​药`（零宽空格），或改用繁体 `槍支`。这些写法在字面上匹配不到词库，也会打断模型的语义信号。

启用 `-normalize`（**默认开启**）后，词库匹配与模型推理都会先把输入还原为标准形式：

| 处理 | 效果 |
|------|------|
| 去除标点、空白、Emoji、零宽字符、下划线 | `炸.药` `炸 药` `炸🔥药` → `炸药` |
| 繁体转简体 | `槍支彈藥` → `枪支弹药`（2956 字对照表，源自 OpenCC） |
| 全角转半角 | `Ｃ４` → `c4` |
| 大小写折叠 | `C4炸药` → `c4炸药` |

命中位置仍指向**原文**，且覆盖被剔除的干扰字符——`这里有炸.药教程` 返回 `[3,6)`，前端高亮能盖住完整的变体写法。

实测：随机抽取词库中 150 个真实词条，中间插入一个点后，关闭归一化时仅 63 条被拦截，开启后 150 条全部拦截。正常文本（`价格是99.5元`、`test.user@example.com`）不受影响。

> 词库与输入用同一套规则归一化，两侧在同一空间比较。`-normalize=false` 可退回纯字面匹配。

繁简对照表由 `scripts/gen_traditional.py` 从 [OpenCC](https://github.com/BYVoid/OpenCC) 生成，Go 与 Python 两份表同源，测试会校验一致性。需要更新时：

```bash
pip install opencc-python-reimplemented
python3 scripts/gen_traditional.py
```

### 语义样本库（补足模型漏报）

模型权重无法在线更新，遇到漏报只能等下一轮微调。词库能补单词，但补不了"换几个字的同类句式"。样本库填补这个空档——把漏报的**整句**提交上来，立即生效。

用 `-samples-file` 启用：

```bash
noblack -samples-file data/samples.json -sample-threshold 0.75
```

| 接口 | 说明 |
|------|------|
| `GET /samples` | 列出全部样本与当前阈值 |
| `POST /samples` | 新增样本（需令牌） |
| `DELETE /samples/{id}` | 删除样本（需令牌） |

```bash
# 提交一条模型漏报的句子
curl -X POST http://localhost:8080/samples \
  -H 'Content-Type: application/json' -H 'X-Auth-Token: <令牌>' \
  -d '{"text":"漏报的整句原文","levels":["违法"],"remark":"模型漏报"}'
```

命中后 `/check` 返回 `decided_by: "sample"`，并在 `sample_matches` 里给出命中的样本与相似度。

**工作原理**：把样本与待检文本都归一化后取字符 bigram 集合，算 Dice 相似度，超过阈值即判定命中。因此它能召回"改动几个字的改写版"，而不只是逐字相同的文本。

**阈值取舍**（`-sample-threshold`，默认 0.75）：

| 阈值 | 效果 |
|------|------|
| 0.6 以下 | 召回激进，同话题但无关的句子也可能被拦，误报风险高 |
| **0.75** | 能容忍替换几个字或调整语序，实测不误拦正常文本 |
| 0.9 以上 | 几乎只匹配逐字相同的文本，失去泛化能力 |

> 样本库只在词库和模型都**没有**拦下来时才参与判定，不会覆盖更精确的归因。样本按归一化文本去重，`炸药教程` 与 `炸.药教程` 视为同一条。

### 检测模式

`-detect-mode` 决定词库与模型两条链路的编排方式：

| 模式 | 行为 |
|------|------|
| `word_only` | 仅词库，完全不调用模型 |
| `model_only` | 仅模型，技术失败时**不回退**词库 |
| `model_first` | 模型优先，仅在模型**技术失败**时回退词库 |
| `word_first` | 词库优先；词库不会技术失败，故模型仅用于未命中召回 |
| `both` | 两条链路并行全跑，任一命中即拦截（默认，兼容历史行为） |

> ⚠️ **"失败" 与 "未命中" 语义不同**：
> **技术失败**（服务不可用/超时）触发 `model_first` 的降级回退；**未命中**（正常返回但判无风险）默认不触发任何回退，需显式开启 `-recall-on-miss` 才补跑另一条链路。
>
> 除 `both` 外的模式均为**串行短路**——优先链路命中即返回，省掉另一条链路的开销。

响应中 `blocked` 为跨链路最终判定，`decided_by` 说明结论来源；`has_sensitive_word` 仅代表词库链路，保留用于兼容旧调用方。

### Docker 环境变量

| 环境变量 | 默认值 | 对应参数 | 说明 |
|----------|--------|----------|------|
| `NB_ADDR` | `:8080` | `-addr` | HTTP 监听地址 |
| `NB_WORDS` | `/data/words.json` | `-words` | 词库文件路径 |
| `NB_STATS` | `/data/stats.json` | `-stats-file` | 统计持久化文件；置空可关闭 |
| `NB_TOKEN` | 空 | `-token` | 词库写操作令牌 |
| `NB_CI` | `false` | `-ci` | 是否忽略英文大小写 |
| `NB_WATCH` | `true` | `-watch` | 是否启用文件监听 |
| `NB_MODEL_SERVICE_URL` | 空 | `-model-service-url` | 可选模型服务地址；为空时仅使用词库匹配 |
| `NB_DETECT_MODE` | `both` | `-detect-mode` | 检测模式；留空使用默认值 |
| `NB_RECALL_ON_MISS` | `false` | `-recall-on-miss` | 未命中时是否补跑另一条链路 |

## 热更新机制

词库更新不会直接修改正在提供查询的自动机：

1. 从 JSON 文件或在线管理接口读取最新词条。
2. 在后台完整构建新的 Aho-Corasick 自动机。
3. 构建成功后，通过 `atomic.Value` 一次性替换当前实例。
4. 已开始的请求继续使用原实例，后续请求使用新实例。

文件监听使用 `fsnotify`，也可以调用 `POST /reload` 主动触发更新。重建操作会串行执行，但不会占用查询路径上的锁。

## 项目结构

```text
noblack/
├── cmd/server/          # 服务入口
├── internal/api/        # HTTP 接口与内嵌控制台
├── internal/matcher/    # 自动机、词库解析与匹配逻辑
├── internal/stats/      # 运行统计与持久化
├── internal/store/      # 词库管理、热替换与文件监听
├── data/words.json      # 默认及运行词库
├── API.md               # 完整接口文档
├── Dockerfile
└── docker-compose.yml
```

## 测试

```bash
# 运行全部测试
go test ./...

# 运行匹配模块基准测试
go test -bench=. ./internal/matcher/
```

实际吞吐会受文本长度、命中数量、词库内容、硬件环境和并发模型影响。建议使用与生产场景接近的数据运行基准测试，再据此设置实例数量和资源限制。

## 使用建议

- 生产环境建议配置 `-token`，并在反向代理层限制管理接口的访问范围。
- 词库与统计文件可能包含业务数据，请合理设置文件权限、备份和保留策略。
- 自动匹配结果适合作为规则判断或人工复核的辅助信息，不应在缺少上下文时作为唯一决策依据。

## 友情链接
[linuxdo](https://linux.do)

## License

本项目采用 [GNU Affero General Public License v3.0](./LICENSE) 许可。

## 请求体大小与管理接口鉴权

- 请求体不超过 3 MiB 时正常处理。
- 请求体超过 3 MiB 且不超过 10 MiB 时，服务端必须已配置令牌，并且请求需要携带该有效令牌。
- 请求体超过 10 MiB 时返回 HTTP 413，携带令牌也不会放行。
- 启用令牌鉴权后，`POST /reload` 和 `POST /stats/reset` 与词库增删改接口使用相同的令牌鉴权。

## AI 双模型（纯 CPU）

项目可选内置 Lite 与 MacBERT 两个模型。默认部署仅使用合并后的词库匹配，不启动模型服务；显式配置 `NB_MODEL_SERVICE_URL` 或使用 `--enable-models` 后，两个模型才会同时常驻内存并并行执行。

生产微调模型默认采用 `max` 策略以保留任一模型发现的色情/谐音风险；如需进一步压低误报，可设置 `NB_MODEL_COMBINE_POLICY=consensus`，要求两个模型共同升级。

```powershell
# Windows（默认仅词库匹配）
.\scripts\start-all.ps1

# Windows（启用可选 CPU 模型）
.\scripts\start-all.ps1 -EnableModels

# Docker（默认仅词库匹配）
docker compose up -d --build
```

打开 `http://127.0.0.1:8080`。详细说明见 [DEPLOY_MODELS.md](./DEPLOY_MODELS.md)。

## Windows/Linux 离线运行包

发布脚本会校验生产模型权重，分别构建 Go 主服务和 PyInstaller 模型服务，并生成带 SHA-256 的原生离线包。模型程序必须在目标操作系统上构建。

#### 新环境初始化

新克隆的仓库缺两样构建必需、但不在版本库里的东西：**LFS 模型权重**（仓库里只有 132 字节的指针）和 **Python 虚拟环境**（`.build/` 在 `.gitignore` 中）。一条命令补齐：

```bash
./scripts/bootstrap.sh
```

它会依次：检查 Go/Python → 拉取 LFS 权重 → 建 venv 并装依赖（torch CPU 版 + requirements + PyInstaller）→ 跑 Go 测试 → 校验发布输入。脚本是幂等的，已就绪的环节会跳过，可反复执行。

常用选项：`--recreate`（依赖版本变更后重建 venv）、`--skip-lfs`、`--skip-venv`。

> 环境已经就绪时不必运行它——下面这段说明的就是复用现有环境的方式。

> ⚠️ **构建前先看这里，不要重新安装依赖。**
>
> 构建用的 Python 虚拟环境已存在于 `.build/` 下（该目录在 `.gitignore` 中，不会提交，但**请勿删除**）：
>
> | 环境 | 路径 | 解释器 |
> |------|------|--------|
> | Linux | `.build/linux-venv/` | `.build/linux-venv/bin/python` |
> | Windows | `.build/windows-venv/` | `.build/windows-venv/Scripts/python.exe` |
>
> 直接用上表的解释器调用 `scripts/release.py`，**无需 pip install**（torch 等依赖有数 GB，重装非常耗时）：
>
> ```bash
> .build/linux-venv/bin/python scripts/release.py build --target linux-amd64
> ```
>
> 检查环境是否可用（而不是去看系统 Python 或项目根目录的 `.venv`）：
>
> ```bash
> .build/linux-venv/bin/python -c "import PyInstaller, torch, transformers; print('ok')"
> ```
>
> **依赖版本以 `model_service/requirements.txt` 为准**（transformers 5.13.1、safetensors 0.8.0、pypinyin 0.55.0），配合 `torch==2.6.0+cpu`。
>
> ⚠️ **Windows 构建必须先清空 `PYTHONPATH`。** 系统环境变量 `PYTHONPATH=K:\site-packages` 指向一个外部 conda 环境（装有 FunASR、ModelScope 等其他项目的依赖），它在 `sys.path` 中排在 venv 自身的 `Lib\site-packages` 之前，会让构建误用 transformers 4.27.4 等旧版本。在 PowerShell 中按下面这样构建（`$env:` 只影响当前会话，不改系统设置）：
>
> ```powershell
> cd D:\work\project\go\noblack
> $env:PYTHONPATH = ""
> .build\windows-venv\Scripts\python.exe scripts\release.py build --target windows-amd64
> ```
>
> 验证构建实际用的版本（应为 5.13.1，而非 4.27.4）：
>
> ```powershell
> $env:PYTHONPATH = ""
> .build\windows-venv\Scripts\python.exe -c "import transformers; print(transformers.__version__, transformers.__file__)"
> ```
>
> 注意 WSL 侧无法覆盖这个变量——它是 Windows 系统级设置，Windows 进程启动时从注册表读取，`env -u PYTHONPATH` 之类的做法无效。

```bash
# Linux x86_64
python scripts/release.py validate
python scripts/release.py build --target linux-amd64
```

```powershell
# Windows 10/11 x64
python scripts\release.py validate
python scripts\release.py build --target windows-amd64
```

PyInstaller 不支持交叉编译。若临时需要在 Linux 上打 Windows 包，可用 `--model-executable` 复用一份已构建好的模型程序：

```bash
.build/linux-venv/bin/python scripts/release.py build --target windows-amd64 \
  --model-executable .build/keep/noblack-model.exe
```

> ⚠️ **`--model-executable` 的路径不能放在 `.build/<target>/` 下。**
> `release.py` 在构建开始时会清空该目录，源文件会在被读取前就被删掉，
> 报错 `model executable missing`。把要复用的 exe 先拷到 `.build/keep/`
> 之类不参与清理的目录再传入。
>
> 上次在 Windows 上原生构建的产物会同时留在 `dist/noblack-windows-amd64/`，
> 那份可以作为复用来源（注意先确认它比 `.build/pyi-dist/` 里的更新——
> 后者可能是很久以前的旧版）。

但这样只有 Go 主服务是新的，**模型服务仍是复用的旧版**——`model_service/` 下的 Python 改动不会进入该包。只要改动了 `model_service/`，就必须按上面的 PowerShell 方式在 Windows 上原生构建。

输出位于 `dist/`：

- `noblack-linux-amd64.tar.gz`
- `noblack-windows-amd64.zip`
- 对应的 `.sha256` 文件和已组装运行目录

运行包提供完整双模型模式和仅词库模式，目标机器无需安装 Go、Python、PyTorch 或 Transformers。
