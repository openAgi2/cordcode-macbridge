# CCCode Bridge Runtime 产品态构建入口

> Phase：P0-02
> 日期：2026-05-07

## 入口决策

Phase 0 保留现有 [go-bridge](/Users/jacklee/Projects/opencode-cc-connect/go-bridge) 目录作为 Go main package，不新增 `cmd/cccode-bridge-runtime/`。原因是当前 runtime 的 server、handlers、events、provider switch 已在同一 package 内形成可测试单元，Phase 0 的目标是冻结产品态入口和构建契约，不做目录级搬迁。

产品态 binary 名称固定为：

```text
cccode-bridge-runtime
```

构建命令：

```bash
cd go-bridge
GOOS=darwin GOARCH=arm64 go build \
  -ldflags "-X main.runtimeVersion=0.1.0-dev -X main.runtimeCommit=$(git rev-parse --short HEAD) -X main.runtimeDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o dist/cccode-bridge-runtime .
```

开发态快速构建仍可使用：

```bash
cd go-bridge && go build -o cccode-bridge-runtime .
```

## 版本输出

runtime 支持：

```bash
./cccode-bridge-runtime -version
```

输出格式：

```text
cccode-bridge-runtime <version> (<commit>, <build-date>)
```

## 默认 Flags

| Flag | 默认值 | 产品态含义 |
|---|---|---|
| `-port` | `8777` | iOS Bridge v1 WebSocket 端口，由 Mac App 管理 |
| `-drivers` | `claude,opencode,codex` | agent/provider target 列表 |
| `-work-dir` | 当前工作目录 | 开发态默认；产品态后续由 Mac App 显式传入 |
| `-codex-backend` | `exec` | Codex runtime 模式 |
| `-codex-app-server-url` | 空 | app-server 模式 URL |
| `-opencode-url` | `http://localhost:64667` | OpenCode 本地服务地址 |
| `-version` | `false` | 打印版本并退出 |

## P0 边界

P0-02 只冻结产品态入口、binary 名称、版本输出和构建命令。数据目录、management API、ready/error frame、shutdown 契约分别由 P0-07/P0-08 落地。
