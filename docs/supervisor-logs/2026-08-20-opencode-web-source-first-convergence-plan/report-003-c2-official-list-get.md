# 开发报告 3 号（C2 official list/get）

> **对应指令**：`directive-003-c2-official-list-get.md`
> **收到时间**：2026-08-20T15:37:26Z
> **来源**：owner 粘贴的开发 agent 完成报告

## 提交

| commit | 内容 | diff |
|---|---|---|
| `69f26bc` | Phase 0 证据门：真实 `/project` raw+sanitized 样本、独立 checker、inventory 行、harness wp 场景 | 5 文件 +699 |
| `ef21db1` | C2 产品实现 + 8 个 owning tests；capture-before-translator | 7 文件 +587/-61 |
| `105b393` | exec-plan C2 三元组 proven、plan 状态、handoff | 3 文件 +35/-22 |

开发 agent 声明工作树干净，三个提交严格按 evidence / product / closeout 分离。

## Phase 0 声明

- raw 来自 pinned official checkout `2cba7e227d` / OpenCode 1.18.18 的隔离 serve，端口 4398/4399，deterministic provider；owner 4096 未动。
- sanitizer 扫描未发现 owner path、Authorization、Basic、4096 或外部账号。
- source mapping：`server-compat.ts:304`、`handlers/project.ts:15-17`、`project/project.ts:336,35-56,217,243-244`、`core/project.ts:105-119`。
- raw 推导：bare array；非 git 目录折叠进 global；有 commit 的 git worktree 形成独立 project；删除 worktree 后 registry 仍保留，因此 missing-worktree 是 CordCode visibility overlay。
- checker 输出：`workspace.project sample ok: entries=3 distinctNonGlobal=2 deletedStillRegistered=True`；`--self-test` PASS。
- 4398/4399 已回收。

## C2 行为声明

- 每个上游 list 是 `GET /session?directory=<dir>&roots=true&limit=100`；数字复用 `core.OpenCodeSessionFetchLimit`。
- OD-1：global/scoped 默认枚举隐藏 archived，by-ID 保留 `ArchivedAt`。
- OD-2：每 worktree scoped fetch；无 headerless list；stable-ID 去重；`ModifiedAt DESC, ID ASC`。
- registry/bucket/malformed 错误整体失败；空 registry 返回空 catalog，零 fallback。
- missing-worktree：global visibility overlay 排除 ghost；显式 scoped 请求保持 scope；by-ID 不受 filesystem 过滤。
- list/get/suggestions 零 POST、零 event subscription，结构上不触达 Kernel/EventPublisher/messages。
- create body 保持 `{}`，未夹带 C3。

## 开发 agent 报告的验证输出

```text
check_workspace_project.py [--self-test]  → ok（entries=3 …）/ PASS
check_samples.py --require-all            → 10/10 captured
go test ./agent/opencode-web/             → ok 2.693s
go test ./go-bridge/ -run 'OpenCodeWeb|ListSessions|SessionCatalog|Projection' → ok 4.689s
go test ./core/                           → ok 0.508s
go vet ./agent/opencode-web/ → clean
go build ./... → OK
```

## Release / process 声明

- `./scripts/build-unsigned-release.sh` 报告 `** BUILD SUCCEEDED **`。
- `/Applications/CordCodeLink.app` 内 runtime commit 为 `ef21db183148`；8777 listener 与 app/runtime 路径均来自 `/Applications`。
- 无临时/DerivedData app；4096 serve PID 71333 未动；无残留诊断进程。

## 停止线与已知边界

- C3 未进入；队列停在 `c3-prompt-impl`。
- 未改 protocol、WireDescriptor、capability、session_pagination、iOS writer/timeline；无 UI automation、真机点击、Relay/VPS 改动。
- 已知未覆盖：v2 `/api/location` 仍由 C1 quarantine；并发 bucket 多错误时返回哪个错误不确定但必失败；`/project` 成功缓存 TTL=15s + SSE invalidation 仍保留。
- 开发 agent 明确未自称监工 verified，等待独立审计。
