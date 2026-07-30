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
Windows 停止：stop.cmd

Linux 完整模式：./start.sh
Linux 仅词库模式：./start-keywords-only.sh
Linux 停止：./stop.sh

默认 Go 服务地址为 http://127.0.0.1:8080。
完整模式还会在 127.0.0.1:8091 启动 Lite 和 MacBERT 双模型服务。
首次启动双模型可能需要较长时间，模型加载完成后才会启动 Go 服务。

配置与数据
----------
复制 config.env.example 为 config.env 后可覆盖端口、线程、阈值、合并策略和令牌。
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
