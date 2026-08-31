# LAN Share 0.9.3

LAN Share 是一个面向 **Windows 7 / Windows 11 / Deepin / Linux / macOS** 的轻量局域网文件分享工具。

设计目标：不依赖中心服务器、不依赖数据库、不要求 Python/Java/Node 运行时；每台机器运行一个 Go 可执行文件，通过浏览器工作台完成设备发现、文件共享和高速传输。

## V0.9.2 · Windows 启动热修复

V0.9.2 修复部分 Windows 机器上默认 TCP 端口 `51888` 无法绑定、双击后立即退出的问题：

- 默认 HTTP 端口调整为 `18888`，避开 Windows 常见动态端口区间。
- UDP 设备发现端口继续保持 `51889`，保证 V0.9/V0.9.1/V0.9.2 之间仍可互相发现。
- HTTP 服务改为先成功绑定端口，再启动托盘、设备发现并输出 `started`，避免“日志显示已启动但实际监听失败”。
- 如果端口上已经运行 LAN Share，第二次启动会识别 `/healthz`，打开已有 Web UI 后正常退出，而不是报端口冲突。
- 如果端口被其他程序占用或被系统保留，会输出明确错误，并提示使用 `-port <port>` 指定其他端口。

V0.9.1 的 Responsive UI、分页、搜索、远程文件浏览、上传/下载、二维码、安全配对和大文件能力全部保留。

## V0.9.3 · Windows Upload Hotfix

- 修复 Windows 下上传临时文件句柄未关闭，导致完成阶段 rename 失败。
- 连续本机上传和附近设备上传使用同一修复路径。
- 新增 Windows CI 回归测试，防止该类句柄问题再次出现。

## 核心能力

- UDP 局域网设备发现（默认 `51889`）
- HTTP 文件传输（默认 `18888`）
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
http://127.0.0.1:18888
```

指定共享目录：

```bash
./lan-share -dir /path/to/share
```

常用参数：

```text
-port 18888
-discovery-port 51889
-dir <共享目录>
-name <设备名称>
-open=true|false
-secure=true|false
-tray=true|false
```

如果指定端口被占用，可改用其他固定端口，例如：

```bash
./lan-share -port 18080
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

仓库使用 GitHub Releases 发布各平台可执行文件。正式 Tag（例如 `v0.9.3`）会构建 Windows、Win7、Linux、macOS 资产并生成 `SHA256SUMS.txt`。
