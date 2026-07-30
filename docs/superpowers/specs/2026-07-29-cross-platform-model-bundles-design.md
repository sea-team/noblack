# Noblack Windows/Linux 双模型运行包设计

## 目标

为 Noblack 生成两个无需预装 Go、Python、PyTorch 或 Transformers 的离线运行包：

- Windows 10/11 x64；
- Linux x86_64，兼容 Ubuntu 22.04 及以上；
- 仅使用 CPU 推理，不包含 GPU/CUDA 运行时；
- 每个平台同时提供“词库 + 双模型”和“仅词库”启动方式。

## 交付方案

每个平台使用两个独立的原生程序：

1. `noblack`：Go 主服务，负责词库匹配、HTTP API 和调用模型服务；
2. `noblack-model`：由 PyInstaller 在目标平台编译的 Python 模型服务，内含 Python、PyTorch、Transformers、Pypinyin、Safetensors 及应用代码。

模型权重不嵌入可执行文件，随运行包放在 `models/` 目录，便于校验和后续替换。Windows 和 Linux 模型程序必须分别在对应平台构建，不能交叉复用。

## 运行架构

完整模式的启动顺序：

1. 启动器读取环境变量或默认配置；
2. 检查词库、Lite 模型和 MacBERT 模型文件；
3. 检查 8080、8091 端口；
4. 启动 `noblack-model`，监听 `127.0.0.1:8091`；
5. 轮询模型服务 `/health`，确认 Lite 和 MacBERT 均完成加载；
6. 启动 `noblack`，监听 `0.0.0.0:8080`；
7. Go 服务先执行词库匹配，再调用本机模型服务完成双模型审核。

仅词库模式跳过模型文件检查和模型进程，直接以空的 `NB_MODEL_SERVICE_URL` 启动 Go 服务。

## 运行包目录

Windows：

```text
dist/noblack-windows-amd64/
├─ noblack.exe
├─ noblack-model.exe
├─ start.cmd
├─ start-keywords-only.cmd
├─ stop.cmd
├─ config.env.example
├─ words.json
├─ models/
│  ├─ lite-production-v1/
│  └─ macbert-production-v1/
├─ data/
├─ logs/
└─ README.txt
```

Linux：

```text
dist/noblack-linux-amd64/
├─ noblack
├─ noblack-model
├─ start.sh
├─ start-keywords-only.sh
├─ stop.sh
├─ config.env.example
├─ words.json
├─ models/
│  ├─ lite-production-v1/
│  └─ macbert-production-v1/
├─ data/
├─ logs/
└─ README.txt
```

Windows 交付 ZIP，Linux 交付 `tar.gz`，并为每个压缩包生成 SHA-256 校验文件。

## 配置与持久化

默认值：

- Go 服务地址：`0.0.0.0:8080`；
- 模型服务地址：`127.0.0.1:8091`；
- 模型设备：CPU；
- 模型并发线程：2；
- 双模型合并策略：`max`；
- 初始词库：包内 `words.json`；
- 可写词库：`data/words.json`；
- 日志目录：`logs/`；
- PID 文件目录：`data/`。

首次运行时，如果 `data/words.json` 不存在，启动器从包内 `words.json` 初始化。后续启动保留 `data/words.json`，允许 Go 服务执行热更新。

端口、词库路径、模型路径、模型线程数、判定阈值、合并策略和鉴权令牌都可通过环境变量覆盖。`config.env.example` 只提供示例，不包含密钥。

## 进程管理与错误处理

- 启动器先检查文件完整性和端口占用；
- 模型服务启动超时时停止已启动进程并返回非零退出码；
- 模型健康检查必须返回 `lite`、`macbert`、`cpu` 和并行执行标识；
- Go 服务启动失败时关闭模型进程；
- 重复运行启动器时根据 PID 和进程状态拒绝重复启动；
- 停止脚本只终止运行包记录的进程，不按进程名批量杀进程；
- PID 失效时清理 PID 文件，不影响其他程序；
- 错误信息写到控制台和 `logs/`，并明确指出缺失文件、占用端口或失败进程；
- 仅词库模式不依赖模型权重或模型程序。

## 模型与依赖

运行包只包含生产模型：

- `models/lite-production-v1`；
- `models/macbert-production-v1`。

四个 Git LFS `model.safetensors` 文件均已恢复并通过仓库记录的 SHA-256 和大小校验。运行包只复制上述两个生产模型权重；每次构建前仍需重复校验权重不是 LFS 指针且摘要一致。

模型程序打包时收集 Transformers 的动态模块、Tokenizer 数据、Torch CPU 动态库以及项目的 `noblack_model` 包。构建结果必须在无 Python 环境的隔离目录中启动验证。

## 构建流程

公共步骤：

1. 校验 Git 工作区和模型权重；
2. 运行 Go 全量测试和 Python 模型测试；
3. 生成合并词库并校验词条数量；
4. 清理独立的 `dist/` 和临时构建目录。

Linux：

1. 以 `GOOS=linux GOARCH=amd64 CGO_ENABLED=0` 构建 Go 服务；
2. 在 Linux Python 环境中用 PyInstaller 构建模型程序；
3. 组装目录、设置可执行权限、生成 `tar.gz` 和 SHA-256。

Windows：

1. 以 `GOOS=windows GOARCH=amd64 CGO_ENABLED=0` 构建 Go 服务；
2. 在 Windows Python 环境中用 PyInstaller 构建模型程序；
3. 组装目录、生成 ZIP 和 SHA-256。

构建缓存、虚拟环境、源码、测试数据和 `.git` 不进入运行包。

## 验证标准

每个平台必须完成：

1. Go 全量单元测试通过；
2. 模型服务自检确认 Lite、MacBERT 均加载并使用 CPU；
3. 完整包在不依赖系统 Python/Go 的环境中启动；
4. `/health` 返回 200，词库数量正确；
5. 普通文本返回无敏感内容；
6. 词库敏感文本产生词条命中；
7. 模型测试文本返回两个 `model_results`，包含 Lite 和 MacBERT；
8. 完整模式启动、停止、重复启动保护正常；
9. 仅词库模式启动、请求和停止正常；
10. 解压后的文件与清单一致，压缩包 SHA-256 校验通过。

测试报告记录平台、架构、程序版本、词库数量、模型加载时间、请求延迟、压缩包大小和 SHA-256。

## 非目标

- 不构建 ARM64；
- 不支持 Windows 7/8；
- 不包含 CUDA/GPU 依赖；
- 不把模型权重塞入单个可执行文件；
- 不修改 Noblack 的审核 API 契约；
- 不删除现有 Docker 部署方式。
