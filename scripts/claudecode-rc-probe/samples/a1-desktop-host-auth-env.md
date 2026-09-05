# Desktop Code tab 活体子进程 host-auth 取证（2026-09-05，非敏感值）

## 活体进程

`Claude-3p/claude-code/2.1.260/claude.app/Contents/MacOS/claude`，
`--resume=<id> --input/output-format stream-json --permission-prompt-tool
stdio --setting-sources=user,project,local ...`（Desktop 1.46388.3，
deploymentMode=3p）。与 think.md 2026-09-05 取证一致：每会话一个私有
CLI 子进程。

## host-auth env 机制（进程 env 提取，值非敏感）

```
CLAUDE_CODE_ENTRYPOINT=claude-desktop-3p
ANTHROPIC_BASE_URL=http://127.0.0.1:15721/claude-desktop
CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST=1
CLAUDE_CODE_HOST_AUTH_ENV_VAR=ANTHROPIC_AUTH_TOKEN
CLAUDE_CODE_HOST_CREDS_FILE=/Users/jacklee/Library/Application Support/Claude-3p/host-creds-b4a8a012-35b1-4956-a32d-63bbb981d361.json
CLAUDE_CODE_HOST_SESSION_ID=local_1375c4a6-0881-4575-be2e-c80e369d1951
CLAUDE_CODE_SDK_HAS_OAUTH_REFRESH=1
CLAUDE_CODE_SDK_HAS_HOST_AUTH_REFRESH=1
CLAUDE_CODE_OAUTH_SCOPES=（空）
CLAUDE_CODE_SUBSCRIPTION_TYPE=（空）
DISABLE_GROWTHBOOK=1          ← Desktop 宿主注入；文档列为 RC 拒绝条件之一
DISABLE_TELEMETRY=（空）
```

未读取 host-creds 文件内容（凭据红线，只记录路径存在）。

## 两个关键链路事实

1. **Desktop 子进程的 API 流量不走官方直连，也不走 user settings 的
   bigmodel URL**：`ANTHROPIC_BASE_URL=http://127.0.0.1:15721/claude-desktop`，
   该端口 listener 为第三方工具 `cc-switch`（PID 95162，本机进程取证）。
   即 Desktop Code tab 会话当前经 cc-switch 本地代理转发（host 注入优先
   于 user settings env 块）。
   → 含义：Desktop 托管会话开 RC 的前置除「开设置」外还包括 provider
   回官方直连；同时 Desktop 自己注入的 `DISABLE_GROWTHBOOK=1` 是否被 RC
   资格检查豁免未知（活体阻塞）。
2. **host-creds 注入是「宿主进程把凭据交给第二个进程」的官方同构先例**：
  Desktop 用 `CLAUDE_CODE_HOST_CREDS_FILE` + `CLAUDE_CODE_HOST_AUTH_ENV_VAR`
   让内嵌 CLI 复用宿主凭据（带属主/权限校验，见 d1 样本）。bridge 若走
   「无头进程复用凭据」路线，等价机制已有官方参照。
