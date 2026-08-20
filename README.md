# LAN Share 0.9

LAN Share 是一个面向 **Windows 7 / Windows 11 / Deepin / Linux / macOS** 的轻量局域网文件分享工具。

设计目标：不依赖中心服务器、不依赖数据库、不要求 Python/Java/Node 运行时；每台机器运行一个 Go 可执行文件，通过浏览器工作台完成设备发现、文件共享和高速传输。

## V0.9 · 局域网文件工作台

V0.9 按用户任务重新组织为四个一级页面：

- **我的空间**：上传文件、搜索/分页查看共享文件、下载、删除、复制临时下载链接、生成二维码。
- **附近设备**：自动发现已启动 LAN Share 的机器，选择指定设备后分页查看其共享文件，支持下载到本机和向该设备上传文件。
- **最近传输**：分页展示发送/接收记录，可按文件/设备、方向和状态筛选。
- **设置**：设备名称、共享目录、开机启动、安全模式、6 位配对码和已授权设备管理。

所有主要列表都支持分页；本机和远端共享文件支持搜索。V0.9 的远端文件分页由本机代理完成，因此可以继续浏览 V0.7/V0.8 设备，无需局域网内所有机器同时升级。

## 核心能力

- UDP 局域网设备发现（默认 `51889`）
- HTTP 文件传输（默认 `51888`）
- 16 MiB 分块上传、失败重试、断点续传
- HTTP Range / 206 大文件下载
- Agent → Agent 直接传输
- 4 路并行拉取（服务端上限 8 路）
- 安全模式与 6 位配对
- 已配对 Agent 传输使用 AES-256-GCM 分块加密
- 临时分享链接和离线二维码
- Windows 原生托盘
- 共享目录持久化和已有文件零复制共享
- Win7 专用 Go 1.20.14 构建

## 快速运行

```bash
go run ./cmd/lan-share
```

默认浏览器地址：

```text
http://127.0.0.1:51888
```

指定共享目录：

```bash
./lan-share -dir /path/to/share
```

常用参数：

```text
-port 51888
-discovery-port 51889
-dir <共享目录>
-name <设备名称>
-open=true|false
-secure=true|false
-tray=true|false
```

## 构建

现代平台：

```bash
sh ./scripts/build-modern.sh
```

输出包括：

- `lan-share-win11-x64.exe`
- `lan-share-deepin-linux-amd64`
- `lan-share-linux-arm64`
- `lan-share-macos-intel`
- `lan-share-macos-apple-silicon`

Windows 7 必须使用 Go 1.20.14：

```bash
GO120=/path/to/go1.20.14/bin/go sh ./scripts/build-win7.sh
```

输出：

- `lan-share-win7-x64.exe`
- `lan-share-win7-x86.exe`

## 安全边界

- 共享访问仅暴露当前共享目录中的文件。
- 切换共享目录、删除文件、修改设置、代理远程下载等管理操作仅允许本机调用。
- 开启安全模式后，远程文件列表、下载和上传都需要先配对。
- 临时分享链接只授权单个指定文件，并带有效期。

## 发布

仓库使用 GitHub Releases 发布各平台可执行文件。正式 Tag（例如 `v0.9.0`）会构建 Windows、Win7、Linux、macOS 资产并生成 `SHA256SUMS.txt`。
