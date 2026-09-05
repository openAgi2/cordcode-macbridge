# D 组凭据静态形状（2026-09-05；活体未验证，见报告阻塞清单）

## 1. OAuth 凭据存储（CLI 2.1.234 strings + 本机只读检查）

- 字段族：`.credentials` ×157、`.credentials.json` ×21、
  `.credentials.access_token` / `.credentials.refresh_token` /
  `.credentials_path` / `.credentials.discardSpentCredentialFile`
- token 字段：`access_token` / `refresh_token` / `scopes` /
  `subscription` / `refresh_token_expires_in` / `refresh_token_expired` /
  `expires_at_unparseable`
- macOS 实际落 keychain（本机检查：keychain 无 `Claude Code-credentials`
  条目、`~/.claude/.credentials.json` 不存在 → 从未登录）。

## 2. Desktop host-creds 注入机制（官方「凭据复用」先例）

- `CLAUDE_CODE_HOST_CREDS_FILE` 带**安全校验**（strings 原文：
  `CLAUDE_CODE_HOST_CREDS_FILE with group/other-readable mode or wrong
  owner` → 属主 + 权限检查）
- 配套：`CLAUDE_CODE_HOST_AUTH_ENV_VAR`（Desktop 用 =ANTHROPIC_AUTH_TOKEN）、
  `CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST=1`、
  `CLAUDE_CODE_SDK_HAS_HOST_AUTH_REFRESH=1` / `CLAUDE_CODE_SDK_HAS_OAUTH_REFRESH=1`

## 3. SDK 0.3.260 bridge.d.ts 的凭据模型（类型面，官方公开）

- `POST /v1/code/sessions/{id}/bridge` → `RemoteCredentials`：
  `{ worker_jwt, api_base_url, expires_in, worker_epoch }`
- **每次调用 bump worker_epoch（调用即 worker 注册）**；worker JWT 有效期
  **4 小时**（注释原文：`JWT is 4h; backend mints a new one every dispatch`）
- `fetchRemoteCredentials(sessionId, baseUrl, accessToken, ...)`：
  **caller 自带 OAuth token**，官方注释明说 thin HTTP wrapper、
  "works from any process (not just the CLI)"
- 失败族（类型定义原文）：
  - `untrusted_device`（403，token 缺失/撤销 → enroll）——Trusted Devices
    相关；`X-Trusted-Device-Token` 头在服务器
    `sessions_elevated_auth_enforcement` 开启时必需（bridge 会话
    SecurityTier=ELEVATED）
  - `session_stale_relogin`（OAuth 会话超过 freshness 窗口 → 重新登录）
  - `oauth_rejected`（401，非终态：换新凭据可重试）
- RC 资格对 token 的硬要求（remote-control.md 原文）：full-scope login
  token；`claude setup-token` / `CLAUDE_CODE_OAUTH_TOKEN` 产生的 token
  "can only make model requests" → 无法建立 RC 会话；`ANTHROPIC_API_KEY`
  设置时须先 unset。

## 4. 未验证（活体阻塞）

- 第二个无头进程实际复用 keychain OAuth token 的并发行为（CLI 多进程共
  享同一条目是否冲突/互踢）
- token 刷新竞争（两进程同时 refresh）
- Trusted Devices（Team/Enterprise）对个人版 Pro/Max 的实际约束
