# 词库管理后端分页实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让词库管理页面通过后端分页、搜索和排序，每次最多加载当前页词条，避免六万余条记录导致浏览器崩溃。

**Architecture:** `Store` 提供线程安全的分页快照查询，HTTP 层校验 `page/page_size/q` 并返回分页元数据，内嵌前端维护页码状态并按需请求。写接口保持不变，写成功后仅刷新当前页。

**Tech Stack:** Go 1.25、`net/http`、原生 JavaScript、Python 3 发布测试、Linux/Windows 离线包。

## Global Constraints

- `GET /words` 默认返回第 1 页、每页 50 条。
- `page` 必须大于等于 1，`page_size` 必须为 1～200。
- 搜索范围包括 `word`、`levels` 和 `remarks`，英文比较忽略大小写。
- `count` 是当前搜索条件的匹配总数，零结果时 `total_pages` 为 0。
- 结果按 `word` 升序稳定排列。
- 浏览器不得保存或渲染完整词库。
- 不改变词库增删改、鉴权、热加载和持久化行为。
- 当前工作直接在用户已授权的 `master` 上执行。

---

### Task 1: 存储层分页快照

**Files:**
- Modify: `internal/store/store_test.go`
- Modify: `internal/store/store.go`

**Interfaces:**
- Consumes: `Store.entries` 的线程安全快照。
- Produces:

```go
type EntryPage struct {
    Entries []matcher.Entry
    Total   int
}

func (s *Store) ListEntriesPage(page, pageSize int, query string) EntryPage
```

- [ ] **Step 1: 写分页、搜索和越界失败测试**

新增测试词条：

```go
entries := []matcher.Entry{
    {Word: "delta", Levels: []string{"Normal"}, Remarks: []string{"fourth"}},
    {Word: "Alpha", Levels: []string{"Safe"}, Remarks: []string{"first"}},
    {Word: "charlie", Levels: []string{"Gambling"}, Remarks: []string{"third"}},
    {Word: "bravo", Levels: []string{"Review"}, Remarks: []string{"SECOND"}},
}
s := New("", entries, matcher.Options{})
```

断言：

```go
page := s.ListEntriesPage(2, 2, "")
// Total == 4，Entries == ["charlie", "delta"]

byWord := s.ListEntriesPage(1, 10, "ALP")
// Total == 1，命中 Alpha

byLevel := s.ListEntriesPage(1, 10, "gambling")
// Total == 1，命中 charlie

byRemark := s.ListEntriesPage(1, 10, "second")
// Total == 1，命中 bravo

beyond := s.ListEntriesPage(3, 2, "")
// Total == 4，Entries 为空
```

- [ ] **Step 2: 运行测试并确认失败**

```bash
GOCACHE=/tmp/noblack-pagination-go-cache \
GOMODCACHE=/tmp/noblack-go-modcache \
go test ./internal/store -run TestListEntriesPage -v
```

Expected: FAIL，提示 `ListEntriesPage` 未定义。

- [ ] **Step 3: 实现最小分页查询**

在锁内使用 `cloneEntries(s.entries)` 获取快照后立即解锁。将 `query` 转为小写，
对 `Word`、每个 `Levels` 和 `Remarks` 元素执行 `strings.Contains`。筛选后按
`Word` 升序排序，计算：

```go
start := (page - 1) * pageSize
if start >= len(filtered) {
    return EntryPage{Entries: []matcher.Entry{}, Total: len(filtered)}
}
end := min(start+pageSize, len(filtered))
return EntryPage{
    Entries: cloneEntries(filtered[start:end]),
    Total:   len(filtered),
}
```

返回空页时必须使用非 `nil` 空切片，确保 JSON 是 `[]` 而不是 `null`。

- [ ] **Step 4: 运行存储层全部测试**

```bash
GOCACHE=/tmp/noblack-pagination-go-cache \
GOMODCACHE=/tmp/noblack-go-modcache \
go test ./internal/store -v
```

Expected: 全部通过。

- [ ] **Step 5: 提交存储层分页**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "feat: 增加词库分页查询"
```

---

### Task 2: GET /words 分页 API

**Files:**
- Modify: `internal/api/handler_test.go`
- Modify: `internal/api/handler.go`

**Interfaces:**
- Consumes: `Store.ListEntriesPage(page, pageSize, query) EntryPage`。
- Produces: `GET /words?page=&page_size=&q=` 分页 JSON 响应。

- [ ] **Step 1: 写默认分页和参数校验失败测试**

增加测试辅助函数，创建 60 个倒序词条：

```go
entries := make([]matcher.Entry, 0, 60)
for index := 60; index >= 1; index-- {
    entries = append(entries, matcher.Entry{
        Word: fmt.Sprintf("word-%02d", index),
        Levels: []string{"common"},
    })
}
```

默认请求 `GET /words` 解码后断言：

```text
HTTP 200
len(words) == 50
count == 60
page == 1
page_size == 50
total_pages == 2
words[0].word == "word-01"
words[49].word == "word-50"
```

自定义请求 `GET /words?page=2&page_size=20` 断言返回 `word-21`～
`word-40`。搜索请求为测试词条增加等级或备注，断言 `q` 只返回匹配记录。

表驱动验证以下请求均返回 HTTP 400：

```text
/words?page=0
/words?page=-1
/words?page=abc
/words?page_size=0
/words?page_size=201
/words?page_size=abc
```

- [ ] **Step 2: 运行 API 测试并确认失败**

```bash
GOCACHE=/tmp/noblack-pagination-go-cache \
GOMODCACHE=/tmp/noblack-go-modcache \
go test ./internal/api -run 'TestWords(Get|List)' -v
```

Expected: 默认请求仍返回 60 条，分页字段缺失，非法参数未返回 400。

- [ ] **Step 3: 实现参数解析与响应**

增加常量：

```go
const (
    defaultWordsPageSize = 50
    maximumWordsPageSize = 200
)
```

实现只接受十进制正整数的解析辅助函数。`handleWords` 的 GET 分支：

```go
page, err := positiveQueryInt(r, "page", 1, 0)
pageSize, err := positiveQueryInt(
    r,
    "page_size",
    defaultWordsPageSize,
    maximumWordsPageSize,
)
query := strings.TrimSpace(r.URL.Query().Get("q"))
result := h.store.ListEntriesPage(page, pageSize, query)
totalPages := 0
if result.Total > 0 {
    totalPages = (result.Total + pageSize - 1) / pageSize
}
```

任一参数错误时返回 HTTP 400 和明确中文消息。成功数据包含：

```go
map[string]interface{}{
    "words":       result.Entries,
    "count":       result.Total,
    "page":        page,
    "page_size":   pageSize,
    "total_pages": totalPages,
}
```

- [ ] **Step 4: 运行 API 和全部 Go 测试**

```bash
GOCACHE=/tmp/noblack-pagination-go-cache \
GOMODCACHE=/tmp/noblack-go-modcache \
go test ./internal/api -v

GOCACHE=/tmp/noblack-pagination-go-cache \
GOMODCACHE=/tmp/noblack-go-modcache \
go test ./...
```

Expected: 全部通过。

- [ ] **Step 5: 提交分页 API**

```bash
git add internal/api/handler.go internal/api/handler_test.go
git commit -m "feat: 增加词库列表分页接口"
```

---

### Task 3: 控制台按页加载与翻页

**Files:**
- Modify: `internal/api/handler_test.go`
- Modify: `internal/api/page.go`

**Interfaces:**
- Consumes: `GET /words` 的 `words/count/page/page_size/total_pages`。
- Produces: 只保存当前页的前端状态和分页控件。

- [ ] **Step 1: 写控制台分页契约失败测试**

对 `GET /` 响应断言：

```go
for _, expected := range []string{
    `id="w-first"`,
    `id="w-prev"`,
    `id="w-page-info"`,
    `id="w-next"`,
    `id="w-last"`,
    `id="w-page-size"`,
    `new URLSearchParams`,
    `page_size`,
    `setTimeout`,
    `300`,
} {
    if !strings.Contains(body, expected) {
        t.Fatalf("missing pagination contract %s", expected)
    }
}
if strings.Contains(body, "ALL_WORDS") {
    t.Fatal("frontend still stores the complete word database")
}
```

同时断言搜索输入调用 `scheduleWordSearch()`，不再调用本地 `renderWords()` 过滤。

- [ ] **Step 2: 运行测试并确认失败**

```bash
GOCACHE=/tmp/noblack-pagination-go-cache \
GOMODCACHE=/tmp/noblack-go-modcache \
go test ./internal/api -run TestIndexWordPagination -v
```

Expected: FAIL，分页控件和请求状态尚不存在。

- [ ] **Step 3: 增加分页 HTML 与样式**

将标题改为由 `w-count-label` 显示总数；筛选框使用
`oninput="scheduleWordSearch()"`。表格后增加分页行和每页条数选择器：

```html
<button id="w-first" class="ghost sm" onclick="goWordPage(1)">首页</button>
<button id="w-prev" class="ghost sm" onclick="goWordPage(WORD_PAGE-1)">上一页</button>
<span id="w-page-info" class="muted">第 1 / 1 页</span>
<button id="w-next" class="ghost sm" onclick="goWordPage(WORD_PAGE+1)">下一页</button>
<button id="w-last" class="ghost sm" onclick="goWordPage(WORD_TOTAL_PAGES)">末页</button>
<select id="w-page-size" onchange="changeWordPageSize(this.value)">
  <option value="20">20 条/页</option>
  <option value="50" selected>50 条/页</option>
  <option value="100">100 条/页</option>
</select>
```

- [ ] **Step 4: 用服务端请求替换全量状态**

使用以下状态：

```javascript
let PAGE_WORDS=[];
let WORD_PAGE=1;
let WORD_PAGE_SIZE=50;
let WORD_TOTAL=0;
let WORD_TOTAL_PAGES=0;
let WORD_LOADING=false;
let WORD_SEARCH_TIMER=null;
let WORD_REQUEST_ID=0;
```

`loadWords()` 用 `URLSearchParams` 发送 `page/page_size/q`。请求序号防止旧搜索响应
覆盖新结果；只把 `d.words` 赋给 `PAGE_WORDS`。`renderWords()` 只渲染
`PAGE_WORDS`。

实现：

```javascript
function scheduleWordSearch(){
  clearTimeout(WORD_SEARCH_TIMER);
  WORD_SEARCH_TIMER=setTimeout(()=>{ WORD_PAGE=1; loadWords(); },300);
}
function goWordPage(page){ /* 校验 1～最大页，更新 WORD_PAGE 并加载 */ }
function changeWordPageSize(size){ /* 更新大小、回到第一页并加载 */ }
function updateWordPager(){ /* 页码、标题、禁用状态 */ }
```

分页加载期间禁用按钮。零结果时显示第 1/1 页，但保留后端
`WORD_TOTAL_PAGES=0` 用于禁用翻页。

- [ ] **Step 5: 调整写操作刷新**

新增、编辑和重载成功后 `await loadWords()`。删除成功后先加载当前页；若当前页
为空且 `WORD_PAGE > 1`，页码减一后再次加载。事件委托继续通过当前页数组索引
获取词条。

- [ ] **Step 6: 运行前端契约和全部 API 测试**

```bash
GOCACHE=/tmp/noblack-pagination-go-cache \
GOMODCACHE=/tmp/noblack-go-modcache \
go test ./internal/api -v
```

Expected: 全部通过，旧的安全事件委托和合并流程测试继续通过。

- [ ] **Step 7: 提交前端分页**

```bash
git add internal/api/page.go internal/api/handler_test.go
git commit -m "feat: 增加词库管理翻页界面"
```

---

### Task 4: 更新 API 文档

**Files:**
- Modify: `API.md`
- Modify: `README.md`

**Interfaces:**
- Consumes: 最终分页接口契约。
- Produces: 可供外部调用方逐页遍历的完整说明。

- [ ] **Step 1: 更新 GET /words 文档**

将“列出全部词条”改为“分页查询词条”，增加 `page/page_size/q` 参数表、默认值、
最大值、HTTP 400 规则及完整响应示例。字段表补充：

```text
data.count
data.page
data.page_size
data.total_pages
data.words[]
```

明确外部调用方必须按 `page` 与 `total_pages` 逐页遍历。

- [ ] **Step 2: 更新 README 接口表**

将 README 中 `GET /words` 说明改为“分页查询词条”，增加一个包含
`page=1&page_size=50&q=` 的调用示例或指向 API 分页章节。

- [ ] **Step 3: 检查文档和格式**

```bash
rg -n '列出全部词条|GET /words' API.md README.md
git diff --check
```

Expected: 不再将 `GET /words` 描述为全量接口；无空白错误。

- [ ] **Step 4: 提交文档**

```bash
git add API.md README.md
git commit -m "docs: 说明词库分页接口"
```

---

### Task 5: 完整验证并重建 dist

**Files:**
- Rebuild: `dist/noblack-linux-amd64`
- Rebuild: `dist/noblack-windows-amd64`
- Preserve: Windows 本地 `config.env` 和日志

**Interfaces:**
- Consumes: 已验证源码和现有 Linux/Windows 模型程序。
- Produces: 内嵌分页控制台的新 Go 程序、双平台归档和 SHA-256。

- [ ] **Step 1: 运行完整源码验证**

```bash
PYTHONPYCACHEPREFIX=/tmp/noblack-pagination-pycache \
python scripts/release.py validate

GOCACHE=/tmp/noblack-pagination-go-cache \
GOMODCACHE=/tmp/noblack-go-modcache \
go test ./...

PYTHONPYCACHEPREFIX=/tmp/noblack-pagination-pycache \
python -m unittest discover -s scripts/tests -v

PYTHONPATH=model_service/src \
PYTHONPYCACHEPREFIX=/tmp/noblack-pagination-model-pycache \
python -m unittest discover -s model_service/tests -v
```

Expected: 发布输入、Go、发布脚本和模型服务全部通过。

- [ ] **Step 2: 复用模型程序重建双平台包**

将现有 `noblack-model` 和 `noblack-model.exe` 临时复制到 `/tmp`，保存 Windows
本地 `config.env` 和日志。分别运行：

```bash
python scripts/release.py build \
  --target linux-amd64 \
  --model-executable /tmp/<linux-model>

python scripts/release.py build \
  --target windows-amd64 \
  --model-executable /tmp/<windows-model.exe>
```

完成后恢复 Windows 本地配置和日志。不得重新构建模型程序或权重。

- [ ] **Step 3: 验证分页页面已进入二进制与归档**

启动 Linux 关键词模式到临时端口，请求 `/` 和
`/words?page=1&page_size=20`，断言页面包含分页控件且接口只返回 20 条。

读取 Windows ZIP 内的 `noblack.exe` 及发布文件列表，确认 ZIP 不包含本地
`config.env`、日志或 PID 文件。

- [ ] **Step 4: 验证清单和归档校验值**

```bash
(cd dist/noblack-linux-amd64 && sha256sum -c SHA256SUMS)
(cd dist/noblack-windows-amd64 && sha256sum -c SHA256SUMS)
sha256sum -c dist/noblack-linux-amd64.tar.gz.sha256
sha256sum -c dist/noblack-windows-amd64.zip.sha256
```

Expected: 全部通过。

- [ ] **Step 5: 最终检查**

```bash
git status --short
git diff --check
git log --oneline 1656f93..HEAD
```

Expected: 源码工作区干净；`dist` 由 `.gitignore` 排除；提交不包含模型权重变化。
