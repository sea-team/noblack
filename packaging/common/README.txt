Noblack 离线运行包
====================

系统要求
--------
Windows 包：Windows 10/11 x64。
Linux 包：Ubuntu 22.04 或更高版本，x86_64。
双模型仅使用 CPU。最低建议 4 核、4 GB 内存，推荐 8 GB 内存。

启动
----
Windows 完整模式：start.cmd
Windows 仅词库模式：start-keywords-only.cmd
Windows 仅启动模型服务：start-model-only.cmd
Windows 停止：stop.cmd

注意：本版随附的 noblack-model.exe 不会读取 config.env，直接双击它启动会固定
监听默认的 8091 端口，config.env 里的 NB_MODEL_PORT 不生效。需要单独启动模型
服务时请用 start-model-only.cmd，它会先读 config.env 再拉起模型服务。

（源码已支持模型服务自读 config.env，但需要用 PyInstaller 重新构建
noblack-model.exe 才会生效，构建步骤见 DEPLOY_MODELS.md。重建后直接双击也可用。）

主程序 noblack.exe 可以直接运行，它会自动读取同目录的 config.env。

Linux 完整模式：./start.sh
Linux 仅词库模式：./start-keywords-only.sh
Linux 停止：./stop.sh

Linux 服务器部署（systemd）
--------------------------
上面的 start.sh 适合临时试跑；生产服务器建议用部署脚本注册为 systemd 服务，
支持开机自启和崩溃自动重拉。以下命令均需 root 权限：

  sudo ./deploy.sh                     完整模式部署（词库 + 双模型）
  sudo ./deploy.sh --keywords-only     仅词库模式部署（不加载模型，省内存）
  sudo ./deploy.sh --help              查看全部选项

常用可选参数：--dir 安装目录（默认 /opt/noblack）、--port 服务端口、
--detect-mode 检测模式、--recall-on-miss 开启未命中召回、--token 鉴权令牌。

部署完成后，安装目录下提供以下管理脚本：

  ./status.sh                查看运行状态与健康检查（无需 root）
  sudo ./restart.sh          重启全部服务
  sudo ./restart.sh --go-only  只重启主服务（改配置后用，不必等模型重新加载）
  sudo ./uninstall.sh        卸载服务，保留词库与配置
  sudo ./uninstall.sh --purge  卸载并删除全部数据（不可恢复）

升级：解压新版发布包后重新执行 deploy.sh 即可，词库和 config.env 会被保留。
日志：journalctl -u noblack -f，或查看安装目录下的 logs/。

默认 Go 服务地址为 http://127.0.0.1:8080。
完整模式还会在 127.0.0.1:8091 启动 Lite 和 MacBERT 双模型服务。
首次启动双模型可能需要较长时间，模型加载完成后才会启动 Go 服务。

配置与数据
----------
复制 config.env.example 为 config.env 后可覆盖端口、线程、阈值、合并策略、令牌和检测模式。
其中 NB_DETECT_MODE 控制词库与模型如何配合（word_only / model_only / model_first / word_first / both），
NB_RECALL_ON_MISS 控制优先链路未命中时是否补跑另一条链路；两项的详细说明见 config.env.example 内注释。
词库位于 data/words.json，随包提供并由服务直接读取、更新。
PID 文件位于 data/，日志位于 logs/。

接口检查
--------
GET  /health
POST /check
Content-Type: application/json

请求示例：
{"text":"需要审核的文本"}

完整性
------
SHA256SUMS 包含包内所有普通文件的 SHA-256。外层压缩包旁的 .sha256 文件用于校验下载或复制后的归档。
