# LAN Share 0.7

LAN Share 是一个面向 **Windows 7 / Windows 11 / Deepin / Linux / macOS** 的局域网文件互传工具。

设计目标：不依赖中心服务器、不依赖数据库、不要求 Python/Java/Node 运行时；每台机器运行一个 Go 可执行文件，浏览器就是 UI。

## 已实现

### V0.7 · Quick Share / LAN AirDrop / Windows tray

- **局域网 AirDrop 模式**：直接把文件拖到设备卡片，不再先从下拉框选目标设备。
- **临时分享链接**：给共享目录中的单个文件生成随机 URL，有效期可选 1 分钟～24 小时，可随时撤销。
- **离线二维码**：二维码由 LAN Share 自己用 Go 标准库生成，不访问互联网、不调用第三方二维码服务。
- 临时分享链接是“单文件能力令牌”：即使开启安全模式，也只放行被分享的那个文件；普通文件列表仍受配对保护。
- 临时链接继续使用 `http.ServeContent`，因此大文件仍支持 Range / 206 和浏览器断点续传。
- **Windows 原生托盘**：Win7/Win11 默认显示通知区域图标，双击打开 LAN Share，右键可打开/退出。实现只使用 Win32 syscall，不引入 CGO。
- Deepin/macOS 继续使用零 CGO浏览器 UI，避免为了托盘引入桌面运行库。
- IPv4 地址优先级调整：优先典型 `192.168.x.x` LAN 地址，减少 VPN / Docker 网卡被选为分享二维码地址的概率。

### V0.4 · 大文件高速与断点

- 16 MiB 分块上传，失败自动重试。
- 上传 ID 稳定，同一文件再次发送会从服务端已保存 offset 继续，不需要从 0 开始。
- `.part` 文件只占实际已接收空间；完成后统一改名。
- 下载继续直接使用 `http.ServeContent(*os.File)`，支持标准 HTTP Range / 206。
- 对“远程共享目录中已存在的大文件”增加 **4 路 / 最多 8 路并行 Range 拉取**，目标端使用 `WriteAt` 并行落盘。
- HTTP Transport 连接复用、关闭压缩、无整文件级 Read/Write timeout。
- UI 实时显示上传速度、ETA 和速度曲线。
- 7 天以上的孤立断点临时文件会自动清理。

### V0.5 · 产品化

- 共享目录可切换并持久化；目录中已有文件直接共享，不重新上传/复制。
- 设备名称可在 UI 中修改并持久化。
- Windows / macOS / Linux(Deepin) 支持“登录系统后自动启动”。
- 最近 100 条传输记录：方向、设备、文件、大小、平均速度、是否加密/续传。
- 一键复制局域网访问地址。
- 手动检查 GitHub Release 新版本。
- GitHub Actions：测试、vet、现代平台交叉编译、Win7 Go 1.20.14 兼容构建、Tag 自动 Release。

> 为保持 **CGO=0、Win7 兼容、单文件跨平台构建**，0.6 没有引入第三方托盘/MenuBar GUI 框架。桌面托盘属于后续可选壳层，不影响传输核心。

### V0.6 · 配对、安全与加密

- 可选“安全模式”。启用后，远程列文件 / 下载 / 上传都必须先配对。
- 6 位运行期配对码，可随时重新生成。
- Agent-to-Agent 配对使用 **X25519 临时密钥交换 + HMAC 配对证明**；访问 Token 不以明文直接返回。
- 已配对 Agent 的分块发送使用 **AES-256-GCM**。
- 已配对 Agent 的并行 Range 高速拉取使用 **AES-256-GCM** 分块响应。
- 信任设备白名单，可在本机 UI 撤销。
- 浏览器远程访问可输入 6 位配对码，获得 30 天 HttpOnly Cookie 会话。
- 管理接口（共享目录、删除、本机设置、配对管理、代理拉取等）始终只允许本机调用。
- 远程 `/api/info` 不暴露真实共享目录路径。

## 快速开始

```bash
./lan-share
```

Windows：

```text
lan-share-win11-x64.exe
```

默认共享/接收目录：

- Windows: `%USERPROFILE%\LANShare`
- Linux/macOS: `~/LANShare`

默认端口：

- TCP `51888`：Web UI / 文件 API
- UDP `51889`：局域网设备发现

浏览器：

```text
http://127.0.0.1:51888
http://本机局域网IP:51888
```

常用参数：

```text
-port 51888
-discovery-port 51889
-dir D:\Share
-name TEST-PC-01
-open=true
-sync=false
-secure=false
-tray=true          # Windows 默认 true；其他平台为 no-op
```

## 大文件传输

### 本机文件发送到另一台设备

```text
Browser File
    │ 16 MiB chunk
    ▼
Local Agent
    │ paired: AES-256-GCM per chunk
    ▼
Remote Agent
    │
    ▼
.part file → final rename
```

每个 chunk 独立提交，因此网络中断后只需要从接收端返回的 offset 继续。

### 拉取对方已经共享的大文件

```text
Remote file
   ├─ Range 0
   ├─ Range 1
   ├─ Range 2
   └─ Range 3
        │
        ▼
Local Agent WriteAt
        │
        ▼
final file
```

默认 UI 使用 4 路，服务端上限 8 路。对于千兆网，单流通常已经接近极限；2.5G/10G + NVMe 环境更可能从并行 Range 获益。

## 安全模式

默认关闭，适合可信测试局域网。启用方式：

1. 在本机页面打开“安全模式”。
2. 对方设备列表会显示“安全 / 未配对”。
3. 点击“配对”，输入对方本机页面上的 6 位配对码。
4. 配对成功后，Agent 保存长期凭据，后续自动认证并加密 Agent-to-Agent 数据分块。

`security.json` 以当前用户权限保存，包含设备 ID 和配对凭据，应按敏感配置文件对待。

浏览器从另一台机器直接访问安全模式设备时，会要求输入配对码；浏览器会话通过 HttpOnly Cookie 保存。

## 防火墙

Windows 管理员身份运行：

```text
scripts\allow-firewall.bat
```

或放行：

- TCP 51888
- UDP 51889

## 构建

现代平台：

```bash
./scripts/build-modern.sh
```

Win7 必须使用 Go 1.20.x；CI 固定使用 Go 1.20.14：

```bash
GO120=/opt/go1.20.14/bin/go ./scripts/build-win7.sh
```

## 验证

```bash
go test ./...
go test -race ./...
go vet ./...
./scripts/build-modern.sh
```

## 工程结构

```text
cmd/lan-share/          程序入口
internal/app/           HTTP、传输、配对、历史、设置
internal/config/        配置持久化
internal/discovery/     UDP 设备发现
internal/fileutil/      文件名与下载辅助
internal/platform/      打开目录/浏览器、开机启动
internal/security/      设备 ID、信任关系、凭据
internal/webui/         内嵌 Web UI
docs/                   架构和协议
scripts/                多平台构建与防火墙脚本
.github/workflows/      CI / Release
```

## 安全边界

LAN Share 0.7 的安全模式主要针对局域网内未授权访问和 Agent-to-Agent 传输内容保护。它不是面向互联网直接暴露设计的公网文件服务器。

远程浏览器配对后的页面仍使用 HTTP；如果需要抵抗同网段被动嗅探，优先使用本机 Agent 发起的已配对 Agent-to-Agent 传输。未来跨公网使用时应增加受信 CA TLS、签名自动更新和更严格的审计策略。
