# 词库统一存放到 data 目录设计

## 背景

当前源码仓库以根目录 `words.json` 保存初始词库，离线包也携带根目录
`words.json`。Linux、Windows 启动器首次运行时再复制为
`data/words.json`。服务实际读写后者，因此运行后的发布目录会同时存在两份
内容相同的词库。

## 目标

源码仓库、开发启动方式和离线发布包统一使用 `data/words.json`：

- 仓库只保留 `data/words.json`；
- 离线包只保留 `data/words.json`；
- 服务默认直接读取并写回 `data/words.json`；
- 不再通过启动器复制根目录词库；
- 显式传入 `-words` 的自定义路径继续有效。

本次不改变词库 JSON 格式、匹配逻辑、热加载机制或词库接口。

## 路径与数据流

仓库中的初始词库从 `words.json` 移动到 `data/words.json`。服务的
`-words` 默认值、开发启动脚本和词库合并工具的默认输出路径同步改为
`./data/words.json`。

发布脚本从仓库 `data/words.json` 读取词库，并直接写入离线包的
`data/words.json`。Linux 和 Windows 启动器启动前检查该文件是否存在；
缺失时给出明确错误并停止，不再从根目录复制。

Docker 镜像仍需处理空挂载目录。构建时从仓库 `data/words.json` 生成镜像内
默认副本，容器入口仅在挂载目标 `NB_WORDS` 不存在时初始化。该副本属于镜像
初始化资源，不会在源码仓库或离线 `dist` 目录中产生第二份词库。

## 发布与兼容

发布校验改为要求仓库存在 `data/words.json`，并拒绝根目录再次出现
`words.json`。生成的 Linux、Windows 包及压缩归档都不得包含根目录
`words.json`。

已有解压目录迁移时，如果 `data/words.json` 已存在，则直接删除根目录
`words.json`；如果仅存在根目录词库，则先移动到 `data/words.json`。当前
Windows 测试目录已有可写词库，因此保留 `data/words.json` 并移除根目录
副本。运行期 `config.env`、日志和 PID 文件不打入发布归档。

命令行显式指定 `-words` 时不受默认路径变化影响。直接运行新版本且依赖旧
默认路径的外部脚本，需要改为 `-words ./data/words.json` 或将词库移动到
`data/`。

## 修改范围

- 移动仓库词库并更新词库来源说明；
- 更新 Go 服务和词库合并工具默认路径；
- 更新开发启动脚本、Docker 构建路径及离线启动器；
- 更新发布校验和组装逻辑；
- 更新 README、API 和离线包说明；
- 重建 `dist` 目录、包内清单、压缩归档及外层 SHA-256。

历史设计和实施记录不回写，以保留当时决策背景；当前用户文档和新设计反映
最新目录结构。

## 测试与验收

1. 发布校验接受唯一的 `data/words.json`，拒绝根目录 `words.json`。
2. 组装后的 Linux、Windows 目录只包含 `data/words.json`。
3. 启动器直接使用 `data/words.json`，不包含根目录复制逻辑。
4. Go、发布脚本、Linux/Windows 启动器及模型服务测试全部通过。
5. 当前 `dist` 解压目录和两个压缩归档均不存在根目录 `words.json`。
6. `SHA256SUMS` 和两个归档外层 `.sha256` 全部校验通过。
