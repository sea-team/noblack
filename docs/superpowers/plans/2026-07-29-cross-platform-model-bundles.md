# Noblack Windows/Linux 双模型运行包实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 构建 Windows 10/11 x64 和 Ubuntu 22.04+ x86_64 的 Noblack 离线运行包，解压后无需安装 Go、Python、PyTorch 或 Transformers，即可运行词库审核或词库加 Lite/MacBERT 双模型审核。

**Architecture:** 每个平台包含一个静态 Go API 程序和一个由 PyInstaller 在目标平台冻结的 Python 模型程序。平台启动器负责校验资源、管理 PID、等待模型健康检查、启动 Go 服务及安全停止；构建脚本负责校验模型摘要、组装目录、生成归档和 SHA-256 清单。

**Tech Stack:** Go 1.25.12、Python 3.11/3.12、Linux PyTorch 2.13.0 CPU、Windows PyTorch 2.8.0 CPU、Transformers 5.13.1、PyInstaller 6.16.0、PowerShell 5.1、POSIX shell。Windows 使用 2.8.0，以规避新版官方 wheel 在部分 Windows 10/11 环境中的 `c10.dll` WinError 1114。

## Global Constraints

- Windows 目标为 Windows 10/11 x64。
- Linux 目标为 x86_64，兼容 Ubuntu 22.04 及以上。
- 仅使用 CPU，不包含 CUDA/GPU 运行时。
- 完整包不得依赖目标机器上的 Go、Python、PyTorch 或 Transformers。
- 包中只包含 `lite-production-v1` 和 `macbert-production-v1` 两个生产模型。
- 不改变现有 `/health`、`/check` 等 HTTP API 契约。
- 保留现有 Docker 部署方式。
- 构建产物写入 `dist/`，中间文件写入 `.build/`，两者不提交 Git。
- 当前仓库 `.git/index` 缺失；未经用户明确授权不得执行 `git reset`。以下提交步骤仅在索引安全恢复后执行。

---

## 文件结构

新增或修改文件及职责：

- `packaging/model-checksums.json`：生产模型权重的固定大小和 SHA-256。
- `packaging/noblack-model.spec`：PyInstaller 模型程序构建定义。
- `packaging/common/config.env.example`：可覆盖配置示例。
- `packaging/common/README.txt`：运行包使用说明模板。
- `packaging/linux/start.sh`：Linux 完整模式启动。
- `packaging/linux/start-keywords-only.sh`：Linux 仅词库模式启动。
- `packaging/linux/stop.sh`：Linux 精确停止 PID 文件记录的进程。
- `packaging/windows/noblack-control.ps1`：Windows 进程、端口、健康检查和 PID 管理。
- `packaging/windows/start.cmd`：Windows 完整模式入口。
- `packaging/windows/start-keywords-only.cmd`：Windows 仅词库入口。
- `packaging/windows/stop.cmd`：Windows 停止入口。
- `scripts/release.py`：跨平台校验、编译、组装、归档和自检编排。
- `scripts/tests/test_release.py`：发布脚本的标准库单元测试。
- `model_service/requirements-build.txt`：固定 PyInstaller 构建依赖。
- `model_service/app.py`：兼容源码运行和 PyInstaller 冻结运行的根目录解析。
- `.gitignore`：忽略 `.build/`、`dist/` 和发布测试缓存。
- `README.md`：增加离线运行包构建和使用说明。

---

### Task 1: 模型权重与发布资源校验

**Files:**
- Create: `packaging/model-checksums.json`
- Create: `scripts/tests/test_release.py`
- Create: `scripts/release.py`
- Modify: `.gitignore`

**Interfaces:**
- Produces: `validate_model_file(root: Path, relative_path: str, size: int, sha256: str) -> None`
- Produces: `validate_release_inputs(root: Path) -> dict[str, int]`
- Produces: CLI `python scripts/release.py validate`

- [ ] **Step 1: 写模型校验失败测试**

在 `scripts/tests/test_release.py` 中使用 `unittest` 创建临时目录，覆盖：

```python
def test_validate_model_file_rejects_lfs_pointer(self):
    model = self.root / "models/lite-production-v1/model.safetensors"
    model.parent.mkdir(parents=True)
    model.write_text("version https://git-lfs.github.com/spec/v1\n", encoding="utf-8")
    with self.assertRaisesRegex(ValueError, "Git LFS pointer"):
        release.validate_model_file(self.root, str(model.relative_to(self.root)), 10, "0" * 64)

def test_validate_model_file_rejects_wrong_digest(self):
    model = self.root / "model.safetensors"
    model.write_bytes(b"model")
    with self.assertRaisesRegex(ValueError, "SHA-256"):
        release.validate_model_file(self.root, "model.safetensors", 5, "0" * 64)
```

- [ ] **Step 2: 运行测试并确认失败**

Run:

```bash
python -m unittest scripts.tests.test_release.ReleaseValidationTests -v
```

Expected: FAIL，提示 `scripts.release` 中尚无 `validate_model_file`。

- [ ] **Step 3: 写固定模型清单和最小校验实现**

`packaging/model-checksums.json` 固定为：

```json
{
  "models/lite-production-v1/model.safetensors": {
    "size": 5361712,
    "sha256": "27fc0b24c3e5a894f315aab2ca76d80129092fd7a74dea3a12a3b52d25e27d28"
  },
  "models/macbert-production-v1/model.safetensors": {
    "size": 411703720,
    "sha256": "9dd249951ebe083a537036ac50155b03b3582de62b07872176e13b98ccc02010"
  }
}
```

`scripts/release.py` 分块计算 SHA-256（8 MiB），拒绝 Git LFS 指针，严格校验大小和摘要，并检查 `words.json`、两个模型目录和模型配置。`validate` 成功时输出两个模型大小和词库字节数。

- [ ] **Step 4: 运行校验测试**

Run:

```bash
python -m unittest scripts.tests.test_release.ReleaseValidationTests -v
python scripts/release.py validate
```

Expected: 测试 PASS；CLI 输出两个模型均校验成功。

- [ ] **Step 5: 更新忽略规则**

在 `.gitignore` 增加：

```gitignore
.build/
dist/
scripts/tests/__pycache__/
```

- [ ] **Step 6: 提交检查点**

索引恢复后执行：

```bash
git add .gitignore packaging/model-checksums.json scripts/release.py scripts/tests/test_release.py
git commit -m "build: validate release model assets"
```

---

### Task 2: 冻结模型服务

**Files:**
- Create: `model_service/requirements-build.txt`
- Create: `model_service/src/noblack_model/runtime_paths.py`
- Create: `model_service/tests/test_runtime_paths.py`
- Create: `packaging/noblack-model.spec`
- Modify: `model_service/app.py`

**Interfaces:**
- Produces: `resolve_package_root() -> Path`
- Produces: PyInstaller output `noblack-model` or `noblack-model.exe`
- Consumes: `NB_PACKAGE_ROOT`、`NB_LITE_MODEL`、`NB_MACBERT_MODEL`

- [ ] **Step 1: 写冻结路径解析测试**

```python
def test_package_root_prefers_environment(self):
    with mock.patch.dict(os.environ, {"NB_PACKAGE_ROOT": str(self.root)}):
        self.assertEqual(runtime_paths.resolve_package_root(), self.root.resolve())

def test_package_root_uses_executable_directory_when_frozen(self):
    executable = self.root / "noblack-model"
    with mock.patch.object(sys, "frozen", True, create=True), \
         mock.patch.object(sys, "executable", str(executable)):
        self.assertEqual(runtime_paths.resolve_package_root(), self.root.resolve())
```

纯路径逻辑放入 `runtime_paths.py`，避免测试导入 `app.py` 时加载模型。

- [ ] **Step 2: 运行测试并确认失败**

Run:

```bash
PYTHONPATH=model_service/src python -m unittest model_service.tests.test_runtime_paths -v
```

Expected: FAIL，提示 `noblack_model.runtime_paths` 不存在。

- [ ] **Step 3: 实现冻结路径兼容**

`resolve_package_root()` 按以下优先级返回：

1. 非空 `NB_PACKAGE_ROOT`；
2. PyInstaller 模式下 `Path(sys.executable).parent`；
3. 源码模式下仓库根目录。

`model_service/app.py` 使用该根目录构造默认模型路径，显式模型环境变量仍具有最高优先级。

- [ ] **Step 4: 添加构建依赖和 PyInstaller spec**

`requirements-build.txt` 固定 `pyinstaller==6.16.0`。Spec 收集 `torch`、`transformers`、`tokenizers`、`safetensors`、`pypinyin` 的子模块、数据和动态库，包含 `noblack_model`、`noblack_data`，排除 CUDA 测试/开发模块，生成控制台模式单文件，不嵌入模型权重。

- [ ] **Step 5: 运行路径和语法测试**

Run:

```bash
PYTHONPATH=model_service/src python -m unittest model_service.tests.test_runtime_paths -v
python -m py_compile model_service/app.py model_service/src/noblack_model/runtime_paths.py
```

Expected: 全部 PASS。

- [ ] **Step 6: 提交检查点**

索引恢复后执行：

```bash
git add model_service/app.py model_service/requirements-build.txt model_service/src/noblack_model/runtime_paths.py model_service/tests/test_runtime_paths.py packaging/noblack-model.spec
git commit -m "build: freeze the dual model service"
```

---

### Task 3: Linux 启停与进程管理

**Files:**
- Create: `packaging/linux/start.sh`
- Create: `packaging/linux/start-keywords-only.sh`
- Create: `packaging/linux/stop.sh`
- Create: `packaging/common/config.env.example`
- Create: `scripts/tests/test_linux_launchers.py`

**Interfaces:**
- Consumes: `NB_ADDR`、`NB_MODEL_PORT`、`NB_MODEL_THREADS`、`NB_MODEL_COMBINE_POLICY`、`NB_TOKEN`
- Produces: `data/noblack.pid`、`data/noblack-model.pid`
- Produces: `logs/noblack.log`、`logs/noblack-model.log`

- [ ] **Step 1: 写 Linux 启动器静态契约测试**

断言脚本使用自身目录作为包根，PID 位于 `data/`，日志位于 `logs/`，完整模式在 Go 前轮询模型 `/health`，仅词库模式传空模型 URL，停止脚本只读取并校验 PID，且不使用 `pkill` 或 `killall`。

- [ ] **Step 2: 运行测试并确认失败**

Run:

```bash
python -m unittest scripts.tests.test_linux_launchers -v
```

Expected: FAIL，提示启动器不存在。

- [ ] **Step 3: 实现 Linux 启停脚本**

完整启动脚本创建数据/日志目录、加载 `config.env`、初始化可写词库、校验旧 PID、检查端口、启动模型、等待最多 180 秒、启动 Go、等待最多 30 秒。停止脚本先 TERM，最多等待 10 秒，再只对同一已验证 PID 发 KILL。任何启动失败均清理本次启动的进程。

- [ ] **Step 4: 运行静态测试和 Shell 语法检查**

Run:

```bash
python -m unittest scripts.tests.test_linux_launchers -v
sh -n packaging/linux/start.sh
sh -n packaging/linux/start-keywords-only.sh
sh -n packaging/linux/stop.sh
```

Expected: 全部 PASS。

- [ ] **Step 5: 提交检查点**

索引恢复后执行：

```bash
git add packaging/common/config.env.example packaging/linux scripts/tests/test_linux_launchers.py
git commit -m "build: add Linux release process control"
```

---

### Task 4: Windows 启停与进程管理

**Files:**
- Create: `packaging/windows/noblack-control.ps1`
- Create: `packaging/windows/start.cmd`
- Create: `packaging/windows/start-keywords-only.cmd`
- Create: `packaging/windows/stop.cmd`
- Create: `scripts/tests/test_windows_launchers.py`

**Interfaces:**
- Produces: `noblack-control.ps1 -Action Start|Stop -Mode Full|Keywords`
- Produces: `data/noblack.pid`、`data/noblack-model.pid`

- [ ] **Step 1: 写 Windows 启动器静态契约测试**

断言 CMD 使用 `%~dp0` 和 PowerShell 5.1；PowerShell 使用 `$PSScriptRoot`、`Start-Process -PassThru`、`Get-CimInstance Win32_Process` 路径校验和 `Invoke-RestMethod` 健康检查；停止不使用 `taskkill /IM`。

- [ ] **Step 2: 运行测试并确认失败**

Run:

```bash
python -m unittest scripts.tests.test_windows_launchers -v
```

Expected: FAIL，提示 Windows 启动器不存在。

- [ ] **Step 3: 实现 PowerShell 控制器和 CMD 入口**

控制器支持 Start/Stop 和 Full/Keywords，创建目录、加载配置、检查端口、启动模型并等待 180 秒、启动 Go 并等待 30 秒、记录 UTF-8 日志和 PID、拒绝重复启动。失败或停止时只操作当前包路径匹配的 PID。

- [ ] **Step 4: 运行测试和 PowerShell 语法检查**

Run:

```bash
python -m unittest scripts.tests.test_windows_launchers -v
powershell.exe -NoProfile -Command '$errors=$null; [System.Management.Automation.Language.Parser]::ParseFile("packaging/windows/noblack-control.ps1",[ref]$null,[ref]$errors) > $null; if($errors.Count){$errors; exit 1}'
```

Expected: 全部 PASS。

- [ ] **Step 5: 提交检查点**

索引恢复后执行：

```bash
git add packaging/windows scripts/tests/test_windows_launchers.py
git commit -m "build: add Windows release process control"
```

---

### Task 5: 发布构建、组装和归档

**Files:**
- Modify: `scripts/release.py`
- Modify: `scripts/tests/test_release.py`
- Create: `packaging/common/README.txt`
- Modify: `README.md`

**Interfaces:**
- Produces: `python scripts/release.py build --target linux-amd64|windows-amd64`
- Produces: `dist/noblack-linux-amd64.tar.gz`、`dist/noblack-windows-amd64.zip`
- Produces: 对应 `.sha256` 和包内 `SHA256SUMS`

- [ ] **Step 1: 写组装和清单测试**

```python
def test_assemble_contains_only_production_models(self):
    release.assemble_package(self.root, self.output, "linux-amd64", self.fake_go, self.fake_model)
    self.assertTrue((self.output / "models/lite-production-v1/model.safetensors").is_file())
    self.assertTrue((self.output / "models/macbert-production-v1/model.safetensors").is_file())
    self.assertFalse((self.output / "models/lite-baseline").exists())
    self.assertFalse((self.output / "models/macbert-pilot").exists())

def test_manifest_is_stable_and_excludes_itself(self):
    release.write_manifest(self.output)
    first = (self.output / "SHA256SUMS").read_bytes()
    release.write_manifest(self.output)
    self.assertEqual(first, (self.output / "SHA256SUMS").read_bytes())
```

- [ ] **Step 2: 运行测试并确认失败**

Run:

```bash
python -m unittest scripts.tests.test_release -v
```

Expected: FAIL，提示组装和清单函数不存在。

- [ ] **Step 3: 实现构建与组装**

发布脚本先校验输入并运行 Go 测试，再以 `GOARCH=amd64 CGO_ENABLED=0 -trimpath -ldflags="-s -w"` 构建 Go。模型程序只允许在目标原生平台调用 PyInstaller。随后复制生产模型、词库、启动器、配置和说明，生成稳定排序的包内清单，最后生成 ZIP 或 tar.gz 及外层摘要。`--skip-model-executable` 仅用于开发诊断，不得作为交付包。

- [ ] **Step 4: 写运行说明**

说明完整/仅词库启动、停止、默认端口、配置、日志/PID/词库位置、API 示例、最低 4 核 4GB/推荐 8GB 内存和首次加载提示。`README.md` 增加构建命令与产物位置。

- [ ] **Step 5: 运行发布单元测试**

Run:

```bash
python -m unittest discover -s scripts/tests -v
python scripts/release.py validate
```

Expected: 全部 PASS。

- [ ] **Step 6: 提交检查点**

索引恢复后执行：

```bash
git add scripts/release.py scripts/tests packaging/common/README.txt README.md
git commit -m "build: assemble portable release archives"
```

---

### Task 6: Linux amd64 完整构建与测试

**Files:**
- Generated: `.build/linux-venv/`
- Generated: `dist/noblack-linux-amd64/`
- Generated: `dist/noblack-linux-amd64.tar.gz`
- Generated: `dist/noblack-linux-amd64.tar.gz.sha256`
- Generated: `dist/test-report-linux-amd64.json`

- [ ] **Step 1: 创建隔离构建环境**

```bash
python -m venv .build/linux-venv
.build/linux-venv/bin/python -m pip install --upgrade pip
.build/linux-venv/bin/python -m pip install --index-url https://download.pytorch.org/whl/cpu torch==2.13.0+cpu
.build/linux-venv/bin/python -m pip install -r model_service/requirements.txt -r model_service/requirements-build.txt
```

- [ ] **Step 2: 运行源码双模型自检**

```bash
PYTHONPATH=model_service/src .build/linux-venv/bin/python model_service/app.py --self-test
```

Expected: `ok=true`、`device=cpu`、`parallel=true`，模型为 `lite` 和 `macbert`。

- [ ] **Step 3: 构建 Linux 包**

```bash
.build/linux-venv/bin/python scripts/release.py build --target linux-amd64
```

- [ ] **Step 4: 测试 Linux 仅词库包**

解压到 `/tmp/noblack-linux-release-test`，使用 18080：

```bash
NB_ADDR=:18080 /tmp/noblack-linux-release-test/start-keywords-only.sh
curl -fsS http://127.0.0.1:18080/health
curl -fsS -X POST http://127.0.0.1:18080/check -H 'Content-Type: application/json' --data '{"text":"博彩"}'
/tmp/noblack-linux-release-test/stop.sh
```

Expected: 健康检查 200、敏感词命中、停止后端口释放。

- [ ] **Step 5: 测试 Linux 双模型包**

使用 18080/18091 启动完整模式，确认模型健康接口包含两个模型和 CPU；Go 响应含两个 `model_results`、`model_device=cpu`、`models_parallel=true`；重复启动被拒绝；停止后两个端口释放。

- [ ] **Step 6: 校验归档并写报告**

```bash
sha256sum -c dist/noblack-linux-amd64.tar.gz.sha256
```

报告记录程序架构、词库数量、模型加载时间、请求延迟、归档大小和 SHA-256。

---

### Task 7: Windows amd64 完整构建与测试

**Files:**
- Generated: `.build/windows-venv/`
- Generated: `dist/noblack-windows-amd64/`
- Generated: `dist/noblack-windows-amd64.zip`
- Generated: `dist/noblack-windows-amd64.zip.sha256`
- Generated: `dist/test-report-windows-amd64.json`

- [ ] **Step 1: 创建 Windows 隔离构建环境**

```powershell
python -m venv .build\windows-venv
.\.build\windows-venv\Scripts\python.exe -m pip install --upgrade pip
.\.build\windows-venv\Scripts\python.exe -m pip install --index-url https://download.pytorch.org/whl/cpu torch==2.8.0+cpu
.\.build\windows-venv\Scripts\python.exe -m pip install -r model_service\requirements.txt -r model_service\requirements-build.txt
```

- [ ] **Step 2: 运行 Windows 源码双模型自检**

```powershell
$env:PYTHONPATH = "$PWD\model_service\src"
.\.build\windows-venv\Scripts\python.exe model_service\app.py --self-test
```

Expected: 两个模型均在 CPU 加载，自检成功。

- [ ] **Step 3: 构建 Windows 包**

```powershell
.\.build\windows-venv\Scripts\python.exe scripts\release.py build --target windows-amd64
```

- [ ] **Step 4: 测试 Windows 仅词库包**

解压到 `D:\temp\noblack-windows-release-test`：

```powershell
$env:NB_ADDR = ":18080"
.\start-keywords-only.cmd
Invoke-RestMethod http://127.0.0.1:18080/health
Invoke-RestMethod -Method Post -ContentType "application/json" -Body '{"text":"博彩"}' http://127.0.0.1:18080/check
.\stop.cmd
```

- [ ] **Step 5: 测试 Windows 双模型包**

以 18080/18091 启动并核对双模型响应、重复启动保护和停止行为。测试进程 PATH 不包含构建虚拟环境，证明目标机无需 Python。

- [ ] **Step 6: 校验 ZIP 并写报告**

使用 `Get-FileHash -Algorithm SHA256` 比较 `.sha256`；报告记录平台、版本、词库数量、加载时间、延迟、ZIP 大小和摘要。

---

### Task 8: 最终回归和交付核对

**Files:**
- Verify: `dist/noblack-linux-amd64.tar.gz`
- Verify: `dist/noblack-windows-amd64.zip`
- Verify: 两个平台测试报告

- [ ] **Step 1: 运行源码测试**

```bash
GOCACHE=/tmp/noblack-go-cache GOMODCACHE=/tmp/noblack-go-modcache go test ./...
python -m unittest discover -s scripts/tests -v
PYTHONPATH=model_service/src python -m unittest discover -s model_service/tests -v
```

- [ ] **Step 2: 核对包内容**

两个包不得包含 `.git`、`.build`、源码测试和 baseline/pilot 模型；必须包含生产模型、词库、配置示例、说明、启停入口和 `SHA256SUMS`。Linux 权限正确，Windows 脚本兼容 PowerShell 5.1。

- [ ] **Step 3: 最终 API 冒烟测试**

分别记录 `/health`、普通文本、`博彩` 和双模型响应，确认 Lite/MacBERT、CPU、并行和合并策略字段。

- [ ] **Step 4: 输出交付摘要**

列出归档绝对路径、大小、SHA-256、测试报告、默认启停命令、建议资源和首次启动时间。Git 索引未恢复时明确说明源码提交仍需单独处理。
