# DSH Driver 实现完成情况报告——独立审计

- **日期**：2026-08-15
- **审计对象**：`docs/2026-08-13-dsh-driver-design完成情况.md`（commit `a2d4f97`，p1–p7 终态收敛版）
- **审计方**：owner 委派的独立审计（监督式纪律：不信任自述，测试亲复跑、源码亲读、日志亲查、artifact 亲验）
- **实现范围核对**：MacBridge `dsh/driver`（`9555562`…`a2d4f97`，16 commit）+ iOS `dsh/driver`（`4e51ef1`、`3bc597a`）——与报告「证据索引」一致。

---

## 1. 结论

**通过，可交 owner 真机验收。** §16 八类门槛全部由审计方当场复跑通过；报告的两处
「事实修正」经独立复核**均属实**（其中一处纠正的是审计方自己开工指令 v2 里的错误事实，
开发 agent 如实报备而非照抄指令，处理正确）；「探测-复用-未启动」产品形态在代码、测试
锁死与生产日志三个层面都成立。无阻断项；2 条非阻断备注见 §4。

---

## 2. §16 八门槛逐项复跑（全部亲自执行）

| # | 门槛 | 审计方复跑命令 | 结果 |
|---|---|---|---|
| 1 | attachment matrix | `go test ./go-bridge/ -run TestAttachmentMatrix -count=1` | ✅ ok |
| 2 | capability truth source | `go test ./agent/... -run 'TestSupportedAttachmentKinds\|TestDeriveCapabilitiesIncludesAttachmentKinds' -count=1`（5 个 agent 包全跑） | ✅ ok |
| 3+4 | seq fixture / notification scope | `go test ./agent/dsh/... -count=1`（全包 6.0s，含 scope_seq 11 场景与 seq 矩阵） | ✅ ok |
| 5+6+7 | delivery fault / process lifecycle / reducer frozen samples | 同上（同包内 `dsh_pipeline_test` 等） | ✅ ok |
| 8 | generator/protocol 双清单 | `DSH_ROOT=…deepseek-harness python3 gen-known-event-types.py` → `OK: source==artifact, 44 types; SHA == HEAD (47f9438)`；`--package-source …/dsh-session/lib/index.js` → `OK: == rc6 artifact, 44 types` | ✅ 双 exit 0 |
| — | go-bridge 全量 | `go test ./go-bridge/... -count=1` | ✅ 6 包全 ok |
| — | iOS | `-only-testing:CCCodeTests/BridgeModelsTests`（iPhone 17 Pro Max 模拟器） | ✅ TEST SUCCEEDED |

---

## 3. 两处「事实修正」的独立复核

### 修正 1：SDK stdio 四包不在用户 dsh 闭包内 → **属实**

- 亲测：`ls …/dsh/node_modules/@deepseek-ai/ | grep -Ei "sdk|spine"` → **空**（全局树确无
  `dsh-sdk-jsonrpc-server/-protocol/-demo`、`dsh-agent-spine-demo`）。
- vendor 仓内四包齐全：`agent/dsh/vendor/@deepseek-ai/` ×4，全部 `0.1.0-rc.6`、MIT，
  `vendor/README.md` 注明 `npm pack` 原样拉取（2026-08-15、未修改）。
- 判定：开工指令 v2 事实 #2「全 family 闭包都在（含 SDK server）」**不完整**——family
  必需包在，但 stdio 层不在 dsh CLI 的依赖闭包里。vendor 范围从「胶水」扩到「四包」是
  必要且最小的实现扩展，机制（vendor + 复用全局树 + 零安装）未出指令框架。

### 修正 2：rc.6 事件清单仍为 44 类 → **属实，且指令 v2 事实 #4 是审计方自己的错误**

- 亲测（node 动态导入全局树 dsh-session 的权威导出）：
  `KNOWN_SESSION_EVENT_TYPES.size == 44`，`tool-workflow/run-start`、`agent-preset/selected`
  均在 Set 内。
- 错误来源已定位：dsh-session 包内有两个位置——`lib/types/known-event-types.js`（基础
  字面量，**39** 条）与 `lib/index.js`（组装后的 Set 导出，**44** 条）。指令 v2 的事实 #4
  （「44→39、移除 5 类」）是审计方用单文件 grep 基础字面量得出的**假象**；开发 agent 的
  双证据（node 导入 + index.js 字面量）与第二 artifact（source 明确标注 `lib/index.js`）
  才是权威口径。
- 语义影响：无——44 全集 ⊇ 运行时可能集合，无新增未知类型，driver fail-closed 分支
  不会误触发。

---

## 4. 产品形态合规（指令 v2 硬边界逐条）

| 硬边界 | 审计证据 | 结果 |
|---|---|---|
| 永不代装（npm install/npx/下载） | 生产代码 grep 零命中；`TestProbeChainNeverInstalls` + `fakeNpmShim`（被要求 install 即失败）锁死；npm 仅用于 `root -g` 只读查询 | ✅ |
| 只读用户环境 | `shadow.go` 代码审查：影子树只写 driver 自有数据目录（`.cccode-macbridge/dsh/`），family 全 symlink 回用户全局树，`~/.dsh` 只读凭据链 | ✅ |
| 源码 checkout 仅 dev | 仅 `DSH_DEV_SOURCE_ROOT` 显式 opt-in，默认链外，命中标注 dev-only | ✅ |
| 未启动诚实 | 未命中全部探测路径 → `AgentStatusNotDetected` 文案链保留 | ✅ |
| managed runtime 已删净 | `ensureRuntimeProject`/`npmInstallFunc` 全仓零命中；演进与撤回记录与 git 历史一致 | ✅ |
| 生产实证 route② | 日志 `dsh: runtime via user-global npm dsh dsh=0.1.0-rc.6 app-boot=0.1.0-rc.6 tree=/opt/homebrew/…`（20:59:40）+ 后续 `backendId=deepseek` RPC 持续服务；`:8777` 监听进程为 `/Applications/CordCodeLink.app/…/cordcode-bridge-runtime -port 8777 -drivers …,deepseek`（正式版） | ✅ |

时间线旁证：20:17:37 旧版注册仍是 `auto-discovered (source checkout)`，20:59:40 起切换为
`via user-global npm dsh`——与 `2755a7f` 重装正式版的时间序吻合，证明生产行为确已收敛到
探测-复用形态。

---

## 5. 执行队列与协议

- `.exec-plan/state/plan-8ba00fc3460a.json`：24 todo **全部 done、0 pending、0 blocked**；
  报告所引 hash `e26aaed664a8` 与 json 内记录一致（审计方未复刻 skill 的规范化序列化
  算法，以 json 自身记录为准）。
- canonical `unified-bridge-protocol.md` attachments 章节与 iOS mirror **byte-identical**
  （md5 比对）。

---

## 6. 非阻断备注（留观，不需返工）

1. **裁量 5（eager relay 淘汰未实现）**：设计 §3.6.3 文本描述 eager+lazy 双路径，实现以
   lazy（pre-send repair）+ abort + cleanup 覆盖五场景，gate 6 测试通过。报告已如实声明。
   接受；若未来出现「死亡 session 长期占位」类现象，优先回查此处。
2. **44/39 的提取口径教训**：known-event-types 的人工 grep 必须落到 `lib/index.js` 的组装
   Set（或直接用 `gen-known-event-types.py --package-source`）；`lib/types/` 下的基础字面量
   是不完整视图。本轮指令方与审计预处理先后踩同一坑，已由脚本权威路径消除。

---

## 7. 未执行项（如实）

- **真实 DeepSeek key 的 turn**：owner 授权事项，按报告文末 5 行矩阵验收（第 1 行已被
  生产日志证实，重点 2–4 行）。
- 审计方**未重复执行**：手工 mock-key E2E turn（报告「本机实测记录」）与 Release 构建流程
  ——以 §16 自动化套件亲复跑 + 生产日志 + 运行中正式版进程为替代证据；真实端到端由
  owner 矩阵第 2 行闭环。
