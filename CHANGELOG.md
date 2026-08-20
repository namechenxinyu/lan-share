# Changelog

## 0.6.0

### V0.4 · Performance / large files

- 新增稳定 Upload ID + offset 协议，支持大文件分块上传和断点续传。
- Web UI 以 16 MiB chunk 顺序发送，失败自动重新查询 offset 并重试。
- 新增 4~8 路并行 Range 拉取远程已共享文件，目标端 `WriteAt` 并行落盘。
- 保留 `http.ServeContent(*os.File)` 下载热路径和 HTTP Range / 206。
- 增加实时速度、ETA、速度曲线和传输历史。
- 自动清理超过 7 天的孤立 `.part` 文件。

### V0.5 · Productization

- 设备名称可修改并持久化，UDP 广播实时更新。
- Windows / macOS / Linux 登录后自动启动支持。
- 最近 100 条发送/接收/拉取记录。
- 复制局域网地址、手动检查 GitHub Release 更新。
- CI 覆盖 Go 1.20.14 和 stable；Tag 自动构建 Release。

### V0.6 · Security

- 可选安全模式：未配对远程端禁止文件列表、下载和上传。
- 6 位配对码、失败尝试限流、信任设备白名单和撤销。
- Agent 配对采用 X25519 临时 ECDH + HMAC 配对证明。
- 配对 Token 通过 ECDH 派生密钥 + AES-GCM 封装返回，不直接明文返回。
- 已配对 Agent 的上传 chunk 使用 AES-256-GCM。
- 已配对 Agent 的并行 Range 拉取使用 AES-256-GCM。
- 远程浏览器支持配对后 HttpOnly Cookie 会话。
- 安全状态随 UDP 发现广播，UI 标注安全/配对状态。

## 0.3.0

- 修复 V0.2 源码与测试版本错位导致的构建失败问题。
- 重构为 `cmd/` + `internal/` 工程结构。
- 完整实现共享目录切换、校验和持久化。
- 管理类 API 限制为本机访问。
- 新增 `/healthz`、CI 和 Release 工作流。

## 0.2.0

- Web UI 增加共享目录切换。
- 共享目录持久化。
- 增加“打开目录”。

## 0.1.0

- 首个局域网文件互传 MVP。
