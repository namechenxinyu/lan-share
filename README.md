# LAN Share 0.9.1

LAN Share 是一个面向 **Windows 7 / Windows 11 / Deepin / Linux / macOS** 的轻量局域网文件分享工具。

设计目标：不依赖中心服务器、不依赖数据库、不要求 Python/Java/Node 运行时；每台机器运行一个 Go 可执行文件，通过浏览器工作台完成设备发现、文件共享和高速传输。

## V0.9.1 · Responsive UI

V0.9.1 在 V0.9 功能基础上重点优化界面一致性和不同分辨率下的可用性，不改变文件传输协议：

- **统一响应式布局**：桌面大屏充分利用空间，1366/1024 自动收缩，平板和手机自动切换导航与单列布局。
- **去除重复状态信息**：局域网/IP 状态统一放到顶部，左侧导航只保留版本信息。
- **统一组件规范**：Card、Button、Input、Table、Pagination、Dialog、Toast 使用一致的尺寸、圆角、边框和间距。
- **我的空间**：上传区域更紧凑，文件列表在小屏自动切换为卡片式布局。
- **附近设备**：桌面采用约 34%/66% 的设备/共享文件布局，小屏自动上下排列。
- **最近传输**：桌面保持表格信息密度，小屏切换为可读性更好的记录卡片。
- **设置**：基础设置与安全设置保持双栏，在窄屏自动切换为单列。

V0.9 已有的分页、搜索、远程文件浏览、上传/下载、二维码、配对与大文件能力全部保留。

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
- 本机/远端共享文件和最近传输分页
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

仓库使用 GitHub Releases 发布各平台可执行文件。正式 Tag（例如 `v0.9.1`）会构建 Windows、Win7、Linux、macOS 资产并生成 `SHA256SUMS.txt`。
