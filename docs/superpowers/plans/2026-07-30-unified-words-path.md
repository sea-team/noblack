# 词库统一存放到 data 目录实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将源码仓库、开发启动方式和离线发布包的唯一词库统一为 `data/words.json`，彻底消除根目录与 `data/` 下的重复词库。

**Architecture:** 仓库中的 `data/words.json` 同时是服务默认词库和发布包输入。离线启动器直接校验并使用包内 `data/words.json`，不再执行首次复制；Docker 继续保留镜像内初始化副本，以兼容空挂载目录。

**Tech Stack:** Go 1.25、Python 3 `unittest`、Bash、PowerShell、Docker、Git LFS、ZIP/TAR/SHA-256。

## Global Constraints

- 仓库和离线包只允许存在 `data/words.json`，不得存在根目录 `words.json`。
- 不改变词库 JSON 格式、匹配逻辑、热加载机制和词库 API。
- 显式传入 `-words` 的自定义路径继续有效。
- Docker 必须继续支持 `NB_WORDS` 指向空挂载目录时初始化默认词库。
- Windows 本地 `dist/noblack-windows-amd64/config.env` 保留 `18080/18091`，但不得打入发布 ZIP。
- `dist` 归档不得包含日志、PID 或其他运行期文件。
- 不改写历史设计和实施记录。

---

### Task 1: 调整发布脚本的词库路径契约

**Files:**
- Modify: `scripts/tests/test_release.py`
- Modify: `scripts/release.py`

**Interfaces:**
- Consumes: 仓库根目录 `Path root`。
- Produces: `validate_release_inputs(root)` 要求 `root/data/words.json`；`assemble_package(...)` 生成 `output/data/words.json`。

- [ ] **Step 1: 先修改发布测试**

将测试夹具中的词库创建改为：

```python
words = self.root / "data" / "words.json"
words.parent.mkdir(parents=True, exist_ok=True)
words.write_text('{"words":[]}\n', encoding="utf-8")
```

将重复词库测试改为先创建 `data/words.json`，再创建根目录 `words.json`，并断言
`validate_release_inputs()` 抛出包含 `root word database` 的 `ValueError`。

在 `test_assemble_contains_only_production_models` 中增加：

```python
self.assertTrue((self.output / "data/words.json").is_file())
self.assertFalse((self.output / "words.json").exists())
```

- [ ] **Step 2: 运行测试并确认按预期失败**

Run:

```bash
PYTHONPYCACHEPREFIX=/tmp/noblack-words-pycache \
python -m unittest scripts.tests.test_release -v
```

Expected: FAIL，失败原因是发布脚本仍读取和复制根目录 `words.json`。

- [ ] **Step 3: 最小修改发布逻辑**

在 `validate_release_inputs()` 中使用：

```python
words_path = root / "data" / "words.json"
root_words_path = root / "words.json"
if root_words_path.exists():
    raise ValueError(
        f"root word database is not allowed; use data/words.json: {root_words_path}"
    )
```

在 `assemble_package()` 中先创建 `output_directory/data`，再复制词库：

```python
data_output = output_directory / "data"
data_output.mkdir()
shutil.copy2(root / "data" / "words.json", data_output / "words.json")
```

删除原来的根目录词库复制和重复创建 `data` 目录代码。

- [ ] **Step 4: 运行发布测试并确认通过**

Run:

```bash
PYTHONPYCACHEPREFIX=/tmp/noblack-words-pycache \
python -m unittest scripts.tests.test_release -v
```

Expected: 所有 `test_release` 测试通过。

- [ ] **Step 5: 提交发布路径契约**

```bash
git add scripts/release.py scripts/tests/test_release.py
git commit -m "refactor: 统一发布包词库路径"
```

---

### Task 2: 移动源码词库并更新默认路径

**Files:**
- Move: `words.json` → `data/words.json`
- Modify: `cmd/server/main.go`
- Modify: `cmd/server/main_test.go`
- Modify: `cmd/merge-word-library/main.go`
- Modify: `cmd/merge-word-library/main_test.go`
- Modify: `scripts/start_all.py`
- Modify: `data/sensitive-word-go/SOURCE.md`
- Modify: `Dockerfile`

**Interfaces:**
- Consumes: 唯一源码词库 `data/words.json`。
- Produces: Go 服务默认 `-words ./data/words.json`；合并工具默认 `-output ./data/words.json`。

- [ ] **Step 1: 为两个 Go 命令增加失败测试**

在 `cmd/server/main_test.go` 增加：

```go
func TestDefaultWordsPathUsesDataDirectory(t *testing.T) {
    if defaultWordsPath != "./data/words.json" {
        t.Fatalf("defaultWordsPath = %q", defaultWordsPath)
    }
}
```

在 `cmd/merge-word-library/main_test.go` 增加：

```go
func TestDefaultOutputPathUsesDataDirectory(t *testing.T) {
    if defaultOutputPath != "./data/words.json" {
        t.Fatalf("defaultOutputPath = %q", defaultOutputPath)
    }
}
```

- [ ] **Step 2: 运行测试并确认按预期失败**

Run:

```bash
GOCACHE=/tmp/noblack-words-go-cache \
GOMODCACHE=/tmp/noblack-words-go-modcache \
go test ./cmd/server ./cmd/merge-word-library
```

Expected: FAIL，提示 `defaultWordsPath` 和 `defaultOutputPath` 未定义。

- [ ] **Step 3: 移动词库并实现默认常量**

执行：

```bash
git mv words.json data/words.json
```

在 `cmd/server/main.go` 中定义并使用：

```go
const defaultWordsPath = "./data/words.json"
```

在 `cmd/merge-word-library/main.go` 中定义并使用：

```go
const defaultOutputPath = "./data/words.json"
```

同步将 `scripts/start_all.py` 的 `-words` 参数改为 `./data/words.json`，将
`data/sensitive-word-go/SOURCE.md` 的 `-base`、`-output` 示例改为
`./data/words.json`，将 `Dockerfile` 改为：

```dockerfile
COPY data/words.json /app/words.default.json
```

Docker 入口的空挂载初始化逻辑保持不变。

- [ ] **Step 4: 运行 Go 测试并验证词库合并工具**

Run:

```bash
GOCACHE=/tmp/noblack-words-go-cache \
GOMODCACHE=/tmp/noblack-words-go-modcache \
go test ./...

go run ./cmd/merge-word-library \
  -dict ./data/sensitive-word-go/sensitive_word_dict.txt \
  -tags ./data/sensitive-word-go/sensitive_word_tags.txt \
  -allow ./data/sensitive-word-go/sensitive_word_allow.txt \
  -deny ./data/sensitive-word-go/sensitive_word_deny.txt \
  -base ./data/words.json \
  -output /tmp/noblack-merged-words.json
```

Expected: Go 测试全部通过；合并工具成功生成 `/tmp/noblack-merged-words.json`，
且不修改仓库词库。

- [ ] **Step 5: 提交源码路径统一**

```bash
git add data/words.json words.json cmd/server/main.go cmd/server/main_test.go \
  cmd/merge-word-library/main.go cmd/merge-word-library/main_test.go \
  scripts/start_all.py data/sensitive-word-go/SOURCE.md Dockerfile
git commit -m "refactor: 将默认词库移动到 data 目录"
```

---

### Task 3: 取消离线启动器复制逻辑

**Files:**
- Modify: `scripts/tests/test_linux_launchers.py`
- Modify: `scripts/tests/test_windows_launchers.py`
- Modify: `packaging/linux/start.sh`
- Modify: `packaging/windows/noblack-control.ps1`
- Modify: `packaging/common/README.txt`

**Interfaces:**
- Consumes: 离线包内预置的 `data/words.json`。
- Produces: 启动前存在性检查；缺失时返回非零并提示 `data/words.json`。

- [ ] **Step 1: 增加启动器契约失败测试**

Linux 测试增加：

```python
def test_full_launcher_uses_single_data_word_database(self) -> None:
    script = self.start_script.read_text(encoding="utf-8")
    self.assertIn('WORDS_FILE="$DATA_DIR/words.json"', script)
    self.assertNotIn('$ROOT/words.json', script)
    self.assertNotIn('cp "$ROOT/words.json"', script)
    self.assertIn('word database is missing', script)
```

Windows 测试增加：

```python
def test_controller_uses_single_data_word_database(self) -> None:
    script = self.controller.read_text(encoding="utf-8-sig")
    self.assertIn('$wordsFile = Join-Path $DataDir "words.json"', script)
    self.assertNotIn('Join-Path $Root "words.json"', script)
    self.assertNotIn("Copy-Item", script)
    self.assertIn("word database is missing", script)
```

- [ ] **Step 2: 运行启动器测试并确认失败**

Run:

```bash
PYTHONPYCACHEPREFIX=/tmp/noblack-words-pycache \
python -m unittest \
  scripts.tests.test_linux_launchers \
  scripts.tests.test_windows_launchers -v
```

Expected: FAIL，失败原因是启动器仍从根目录复制词库。

- [ ] **Step 3: 修改 Linux 和 Windows 启动器**

Linux 定义：

```bash
WORDS_FILE="$DATA_DIR/words.json"
```

创建 `data`、`logs` 后执行：

```bash
if [ ! -f "$WORDS_FILE" ]; then
  echo "[noblack] word database is missing: $WORDS_FILE" >&2
  exit 1
fi
```

并让 `go_args` 使用 `"$WORDS_FILE"`。

Windows 保留 `$wordsFile = Join-Path $DataDir "words.json"`，删除
`Copy-Item` 初始化，改为：

```powershell
if (-not (Test-Path -LiteralPath $wordsFile -PathType Leaf)) {
    throw "word database is missing: $wordsFile"
}
```

将 `packaging/common/README.txt` 改为说明 `data/words.json` 随包提供并由服务
直接读写，不再描述首次复制。

- [ ] **Step 4: 运行启动器测试并确认通过**

Run:

```bash
PYTHONPYCACHEPREFIX=/tmp/noblack-words-pycache \
python -m unittest \
  scripts.tests.test_linux_launchers \
  scripts.tests.test_windows_launchers -v
```

Expected: Linux 和 Windows 启动器契约测试全部通过。

- [ ] **Step 5: 提交启动器修改**

```bash
git add packaging/linux/start.sh packaging/windows/noblack-control.ps1 \
  packaging/common/README.txt scripts/tests/test_linux_launchers.py \
  scripts/tests/test_windows_launchers.py
git commit -m "refactor: 离线包直接使用 data 词库"
```

---

### Task 4: 同步当前文档和配置说明

**Files:**
- Modify: `README.md`
- Modify: `API.md`
- Modify: `packaging/common/config.env.example`

**Interfaces:**
- Consumes: 最终路径 `data/words.json`。
- Produces: 用户文档不再指向根目录词库；配置示例继续由发布脚本复制到两个包。

- [ ] **Step 1: 添加文档路径扫描**

Run:

```bash
rg -n '默认.*words\\.json|\\./words\\.json|根目录.*words\\.json|包内 words\\.json' \
  README.md API.md packaging/common/README.txt \
  data/sensitive-word-go/SOURCE.md
```

Expected: 命中旧路径，证明文档尚未统一。

- [ ] **Step 2: 修改当前用户文档**

将 README 的默认路径、启动示例和目录树改为 `./data/words.json`；将 API 文档
中的默认词库、直接编辑说明改为 `data/words.json`。保留 API 中泛指
“磁盘上的 words.json”的描述，仅修正表示具体默认位置的内容。

保留并提交 `packaging/common/config.env.example` 中已完成的逐项中文配置说明，
其中热加载说明继续指向 `data/words.json`。

- [ ] **Step 3: 重新扫描并检查格式**

Run:

```bash
if rg -n '\\./words\\.json|根目录.*words\\.json|包内 words\\.json' \
  README.md API.md packaging/common/README.txt \
  data/sensitive-word-go/SOURCE.md; then
  exit 1
fi

git diff --check
```

Expected: 不再命中旧默认路径；`git diff --check` 无输出。

- [ ] **Step 4: 提交文档和配置说明**

```bash
git add README.md API.md packaging/common/config.env.example \
  packaging/common/README.txt data/sensitive-word-go/SOURCE.md
git commit -m "docs: 更新 data 词库和配置说明"
```

---

### Task 5: 运行完整源码验证

**Files:**
- Verify only.

**Interfaces:**
- Consumes: Tasks 1–4 的全部修改。
- Produces: 可用于重建离线包的已验证源码状态。

- [ ] **Step 1: 验证唯一词库路径**

Run:

```bash
test -f data/words.json
test ! -e words.json
python scripts/release.py validate
```

Expected: 唯一词库存在于 `data/words.json`；发布输入验证成功。

- [ ] **Step 2: 运行 Go 测试**

Run:

```bash
GOCACHE=/tmp/noblack-words-go-cache \
GOMODCACHE=/tmp/noblack-words-go-modcache \
go test ./...
```

Expected: 所有 Go 包测试通过。

- [ ] **Step 3: 运行发布和启动器测试**

Run:

```bash
PYTHONPYCACHEPREFIX=/tmp/noblack-words-pycache \
python -m unittest discover -s scripts/tests -v
```

Expected: 所有发布、Linux 和 Windows 启动器测试通过。

- [ ] **Step 4: 运行模型服务测试**

Run:

```bash
PYTHONPATH=model_service/src \
PYTHONPYCACHEPREFIX=/tmp/noblack-words-model-pycache \
python -m unittest discover -s model_service/tests -v
```

Expected: 所有模型服务测试通过。

---

### Task 6: 迁移并重建 dist

**Files:**
- Move: `dist/noblack-linux-amd64/words.json` → `dist/noblack-linux-amd64/data/words.json`
- Delete: `dist/noblack-windows-amd64/words.json`
- Preserve: `dist/noblack-windows-amd64/data/words.json`
- Sync: 两个平台的 `config.env.example`
- Rebuild: 两个平台的 `SHA256SUMS`、压缩归档和外层 `.sha256`

**Interfaces:**
- Consumes: 已验证源码配置和当前预编译二进制、模型文件。
- Produces: 只包含 `data/words.json` 的 Linux/Windows 解压目录及发布归档。

- [ ] **Step 1: 比较重复词库后迁移**

Run:

```bash
cmp dist/noblack-windows-amd64/words.json \
  dist/noblack-windows-amd64/data/words.json
mkdir -p dist/noblack-linux-amd64/data
mv dist/noblack-linux-amd64/words.json \
  dist/noblack-linux-amd64/data/words.json
rm dist/noblack-windows-amd64/words.json
```

Expected: Windows 两份词库内容一致后删除根目录副本；Linux 词库移动成功。

- [ ] **Step 2: 同步配置示例**

Run:

```bash
cp packaging/common/config.env.example \
  dist/noblack-linux-amd64/config.env.example
cp packaging/common/config.env.example \
  dist/noblack-windows-amd64/config.env.example
```

Expected: 两个平台配置示例与源码完全一致；Windows 本地 `config.env` 不变。

- [ ] **Step 3: 重建包内清单和归档**

重建 Linux 清单和归档。Windows 重建前临时移出 `config.env`、`logs/` 和 PID
文件，但保留并打包 `data/words.json`；完成后原样恢复运行期文件。调用现有：

```python
release.write_manifest(package_directory)
release.create_archive(package_directory, target, root / "dist")
```

不得调用完整模型构建，也不得覆盖现有二进制和四个模型权重。

- [ ] **Step 4: 验证目录、归档和所有校验值**

Run:

```bash
test ! -e dist/noblack-linux-amd64/words.json
test ! -e dist/noblack-windows-amd64/words.json
test -f dist/noblack-linux-amd64/data/words.json
test -f dist/noblack-windows-amd64/data/words.json
sha256sum -c dist/noblack-linux-amd64.tar.gz.sha256
sha256sum -c dist/noblack-windows-amd64.zip.sha256
```

另外读取 TAR/ZIP 文件列表，断言两个归档都包含
`noblack-*/data/words.json`，且不包含 `noblack-*/words.json`、
`noblack-*/config.env`、日志或 PID 文件。分别在两个解压目录执行
`sha256sum -c SHA256SUMS`。

Expected: 路径、归档内容及所有 SHA-256 校验全部通过。

---

### Task 7: 最终审查和交付

**Files:**
- Verify only.

**Interfaces:**
- Consumes: 源码提交和已重建的忽略目录 `dist`。
- Produces: 可提交/推送的最终状态报告。

- [ ] **Step 1: 检查 Git 修改范围**

Run:

```bash
git status --short
git diff --check
git diff --stat 3e85c6c..HEAD
```

Expected: 无意外文件、无空白错误；`dist` 仍由 `.gitignore` 排除。

- [ ] **Step 2: 检查提交历史**

Run:

```bash
git log --oneline 3e85c6c..HEAD
```

Expected: 提交分别覆盖发布契约、源码路径、启动器和文档，不包含模型权重变化。

- [ ] **Step 3: 汇总验证证据**

记录 Go、发布脚本、模型服务、目录唯一性、归档内容和 SHA-256 的实际通过数量。
如任一命令失败，先修复并重新运行对应完整命令，不得以部分测试替代。
