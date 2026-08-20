# LAN Share Architecture

## 目标

LAN Share 面向 Windows 7、Windows 11、Deepin/Linux 和 macOS：

1. 无中心服务器。
2. 单个 Go 可执行文件，CGO=0。
3. 内嵌 Web UI，避免维护四套桌面 GUI。
4. 大文件内存占用与总文件大小无关。
5. 支持中断恢复、高速局域网和可选安全模式。

## 组件

```text
┌────────────────────────────────────────────────────┐
│                    LAN Share                       │
│                                                    │
│ cmd/lan-share                                      │
│      │                                             │
│      ▼                                             │
│ internal/app                                       │
│   ├── resumable upload / parallel pull             │
│   ├── pairing / auth / AES-GCM                     │
│   ├── settings / history                           │
│   ├── internal/config                              │
│   ├── internal/discovery ── UDP 51889              │
│   ├── internal/security                            │
│   ├── internal/platform                            │
│   └── internal/webui                               │
│                                                    │
│ TCP 51888 : UI / file API / pairing                │
└────────────────────────────────────────────────────┘
```

## V0.4 大文件路径

### 可恢复上传

浏览器使用稳定的 Upload ID。接收端临时文件：

```text
.lanshare-upload-<id>.part
```

初始化返回当前 `offset`；客户端从该 offset 继续发送 16 MiB chunk。文件完成后校验最终大小并短暂进入 `finalizeMu` 临界区，确定不冲突的最终文件名再 Rename。

切换共享目录时，进行中的 session 保持原目录快照，新 session 使用新目录。

### 并行 Range 拉取

本机 Agent 拉取另一台 Agent 已共享文件时：

- HEAD 获取总大小；
- 临时文件先 `Truncate(size)`；
- 以 8 MiB range 为任务单元；
- 4~8 个 worker 并发 GET；
- 使用 `WriteAt` 写入目标偏移；
- 全部完成后 Rename。

普通开放模式使用标准 `/api/download` + `Range`；已配对模式使用 `/api/secure-range`，每个 8 MiB range 独立 AES-GCM 加密。

## V0.5 运行与产品化

配置目录保存：

```text
config.json    # share_dir / name / secure_mode
security.json  # device_id / trusted peers / outgoing credentials
```

共享目录和设备名可以在 UI 中修改。自动启动通过系统原生机制实现：

- Windows: `HKCU\...\Run`
- macOS: `~/Library/LaunchAgents/com.lanshare.app.plist`
- Linux/Deepin: `~/.config/autostart/lan-share.desktop`

核心仍保持 CGO=0，因此不把托盘 GUI 强行耦合到传输服务。

## V0.6 安全模型

### 两类访问

**本机管理请求**：loopback 或本机网卡 IP，可切目录、删除、修改设置、发起配对、代理拉取。

**远程文件请求**：安全模式关闭时允许；安全模式开启时必须持有有效配对 Token/Cookie。

### Agent 配对

```text
Initiator                         Receiver
   │                                 │
   │ X25519 client pub + HMAC proof  │
   ├────────────────────────────────>│
   │                                 │ verify 6-digit code proof
   │                                 │ generate X25519 server key
   │                                 │ ECDH shared secret
   │                                 │ generate access token
   │ server pub + AES-GCM(token)     │
   │<────────────────────────────────┤
   │ ECDH + code derive same key     │
   │ decrypt/store token             │
```

配对码不直接作为长期密钥；长期 Token 随机生成并存储在用户私有配置中。

### 数据加密

已配对 Agent 上传：每个 <=64 MiB chunk（UI 默认 16 MiB）独立随机 nonce，AES-256-GCM 认证加密。

已配对 Agent 高速拉取：每个 8 MiB range 独立随机 nonce，AES-256-GCM 后返回。本机解密后 `WriteAt`。

### 浏览器远程访问

远程浏览器无法像 Agent 一样安全保存长期 Agent 凭据，因此通过 6 位配对码换取 HttpOnly SameSite Cookie。浏览器与远端 Agent 之间仍是 HTTP；安全模式主要阻止未授权访问。需要抵抗同网段被动嗅探时，应优先使用本机 Agent-to-Agent 路径。
