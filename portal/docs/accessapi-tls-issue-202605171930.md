> **DRAFT** - 问题记录，暂不实施

# AccessAPI TLS 兼容性问题

**日期**: 2026-05-17
**状态**: 已确认，暂不实施修复

## 问题描述

Portal 的"获取模型列表"功能（`fetchProviderModels`）对 provider.example.com 返回 502 错误。根因是 Go 的 `crypto/tls` 无法与 provider.example.com 服务器完成 TLS 握手。

## 详细测试结果

从 AWS ap-southeast-2 服务器测试（2026-05-17）：

| 客户端 | TLS 1.3 | TLS 1.2 | 备注 |
|--------|---------|---------|------|
| Alpine wget (busybox) | 间歇成功 | 失败 | 服务器不稳定，约 20% 成功率 |
| OpenSSL 3.1.8 | 间歇成功 | 失败 | 同上 |
| Go crypto/tls (CGO_ENABLED=0) | 始终失败 | 始终失败 | TLS 1.3 返回 alert 80 (internal_error)，TLS 1.2 返回 alert 40 (handshake_failure) |

Go 的 `crypto/tls` 无论设置什么 `CurvePreferences`、`CipherSuites`、`MinVersion`/`MaxVersion` 组合，都无法成功握手。

## 根因分析

1. **provider.example.com 服务器 TLS 实现不稳定**：即使 OpenSSL 也只有约 20% 的成功率
2. **Go crypto/tls ClientHello 格式与 OpenSSL 不同**：Go 发送的 ClientHello 包含不同的扩展（如 ALPN、supported_versions 等），provider.example.com 服务器对此更敏感，直接拒绝
3. **CGO_ENABLED=0 的 Go 二进制使用内置 crypto/tls**，不调用系统 OpenSSL，所以无法利用 OpenSSL 的兼容性

这不是 Go 的 bug，而是 provider.example.com 服务端 TLS 实现对 ClientHello 格式过于敏感。

## 建议方案（暂不实施）

### 方案 A：CGO_ENABLED=1 + OpenSSL 动态链接

修改 Dockerfile，使用 CGO_ENABLED=1 编译并链接 Alpine 的 OpenSSL：

```dockerfile
FROM golang:1.24-alpine AS builder
RUN apk add --no-cache gcc musl-dev
RUN CGO_ENABLED=1 go build -o portal ./cmd/server
```

**优点**：Go 的 `crypto/tls` 仍为内置版本，但可通过 `github.com/ReforgedHub/go-openssl` 等库替换 transport
**缺点**：增加构建复杂度；Alpine musl + CGO 有兼容性陷阱；二进制体积增大；仍需额外代码替换 HTTP transport

### 方案 B：Nginx 反向代理特定 Provider

在 nginx 配置中为 provider.example.com 添加反向代理：

```nginx
location /proxy/accessapi/ {
    proxy_pass https://provider.example.com/;
    proxy_ssl_verify off;
}
```

Portal 的 `fetchProviderModels` 对 accessapi 请求改为走 `http://localhost/proxy/accessapi/v1/models`。

**优点**：利用 nginx 的 OpenSSL 处理 TLS，Portal 代码改动最小
**缺点**：需要在 nginx 配置中维护 provider 代理规则；增加一跳延迟

### 方案 C：子进程调用 curl

在 `fetchProviderModels` 中，对已知 TLS 不兼容的 provider，用 `os/exec` 调用系统 curl 获取模型列表：

```go
if tlsIncompatible[provider] {
    cmd := exec.Command("curl", "-s", "-k", modelsURL)
    // ...
}
```

**优点**：利用系统 curl 的 OpenSSL；实现简单
**缺点**：依赖容器内有 curl；进程调用开销；安全性需注意参数注入

### 方案 D：手动维护模型列表

不通过 API 获取 accessapi 的模型列表，直接在 Portal 管理界面手动添加模型。

**优点**：零开发成本；accessapi 模型列表变化不频繁
**缺点**：需要人工维护；无法自动同步

## 推荐方案

短期：**方案 D**（手动维护），已通过"添加到服务模型"功能支持。

中长期：**方案 B**（nginx 代理），如果 accessapi 模型列表需要自动同步，在 nginx 中添加 provider 代理规则是最干净的方案。

## 相关代码

- `internal/routes/models.go:1741` - `fetchProviderModels` handler
- `internal/routes/models.go:1849` - TLS 配置和重试逻辑
- `Dockerfile:20` - `CGO_ENABLED=0` 构建配置