# LAN Share Protocol

当前应用版本：`0.7.0`  
UDP discovery magic：`LANSHARE/1`

## HTTP 51888

### 基础

- `GET /api/info`
- `GET /api/devices`
- `GET /healthz`

安全模式下远程 `/api/info` 不返回共享目录和配对码。

### 文件列表与下载

- `GET /api/files`
- `DELETE /api/files?name=<file>`（仅本机）
- `GET|HEAD /api/download?name=<file>`

`/api/download` 支持标准 `Range` / `206 Partial Content`。

安全模式开启后，远程 `GET /api/files` 和 `/api/download` 需要 Bearer Token 或已配对浏览器 Cookie。

## Resumable upload

### `POST /api/uploads/init`

```json
{
  "id": "u4d8f...",
  "name": "big.iso",
  "size": 53687091200
}
```

返回：

```json
{
  "id": "u4d8f...",
  "offset": 17179869184,
  "size": 53687091200,
  "chunk_size": 16777216,
  "resumed": true
}
```

### `PUT /api/uploads/chunk?id=<id>&offset=<offset>`

请求体是一个 chunk。服务端要求 offset 与已保存 offset 一致。

### `POST /api/uploads/complete?id=<id>`

仅当临时文件大小等于声明 size 时完成并 Rename。

### `DELETE /api/uploads/abort?id=<id>`

删除当前断点临时文件。

### Compatibility

`PUT /api/upload?name=<file>` 保留为单流兼容上传接口。

## Local relay

以下接口只允许本机调用，浏览器 UI 通过它把 chunk 发送给目标 Agent：

- `POST /api/relay/init?device_id=<id>`
- `PUT /api/relay/chunk?device_id=<id>&id=<upload>&offset=<offset>`
- `POST /api/relay/complete?device_id=<id>&id=<upload>`

如果目标已有配对凭据，relay chunk 使用：

```text
Authorization: Bearer <token>
X-LAN-Encrypted: aes-gcm-chunk-v1
X-LAN-Nonce: <base64url nonce>
```

## Parallel pull

### `GET /api/peer-files?device_id=<id>`

本机代理读取远端文件列表。

### `POST /api/pull`

```json
{
  "device_id": "...",
  "name": "big.iso",
  "parallel": 4
}
```

并行度范围 1~8。开放模式使用标准 Range；已配对模式使用加密 range。

### `GET /api/secure-range`

已配对 Agent 专用：

```text
/api/secure-range?name=<file>&start=<n>&end=<n>
Authorization: Bearer <token>
```

每次最大 8 MiB，响应使用 AES-256-GCM，并返回 `X-LAN-Nonce`。

## Pairing

### Agent-to-Agent `POST /api/pair`

请求包含：

- initiator device ID/name
- X25519 ephemeral public key
- `HMAC-SHA256(pair_code, client_pub || 0x00 || device_id)` proof

响应包含：

- receiver device ID/name
- receiver X25519 public key
- AES-GCM nonce
- ECDH 派生密钥加密后的随机 access token

### Browser `POST /api/browser-pair`

远程浏览器提交 6 位码；成功后设置 HttpOnly SameSite Cookie，有效期 30 天。

### Local pairing control

- `POST /api/pair-device`
- `GET|PUT /api/security`
- `POST /api/trust/revoke`

均只允许本机。

## Settings

- `PUT /api/share-dir`（本机）
- `POST /api/open-dir`（本机）
- `GET|PUT /api/settings`（本机）
- `GET /api/history`（本机）
- `GET /api/update-check`（本机，需要互联网）

## UDP 51889 discovery

每 3 秒广播：

```json
{
  "magic": "LANSHARE/1",
  "id": "persistent-device-id",
  "name": "TEST-PC",
  "port": 51888,
  "os": "windows",
  "arch": "amd64",
  "secure": true
}
```

接收端以 UDP 源 IP 作为实际设备 IP，设备约 12 秒未出现即过期。

## V0.7 Quick Share

以下管理接口只允许本机调用：

- `GET /api/share-links`：列出当前未过期分享。
- `POST /api/share-links`：创建分享，JSON: `{"name":"file.iso","ttl_seconds":600}`。
- `DELETE /api/share-links?token=<token>`：立即撤销。
- `GET /api/share-links/qr?token=<token>`：返回离线生成的 `image/png` QR Code。

公开临时下载：

- `GET|HEAD /s/<random-token>`

Token 仅绑定一个文件并保存在内存中；进程重启即失效。下载继续支持 `Range` / `206`。安全模式开启时，这条 URL 不需要设备配对，但不会授权 `/api/files` 或其他文件。

