# 监工指令 14 号：Owner UI reasoning hydrate 修复

> **触发**：owner 真机验收——打开任意抽样 OpenCode session 均显示“无法加载会话投影”；其他 backend 正常  
> **路线图**：canonical §4.1.2 E2b、§6.3、§8 message-page acceptance  
> **范围**：MacBridge `opencode-web` HTTP history/hydrate + owning tests；iOS 不变  
> **kind**：owner-acceptance repair

## 0. 已钉死的根因，不得重新猜测

监工已独立对齐真实日志、4096 只读 HTTP、同版本官方源码与 bridge mapper：

1. iOS 的 `get_session_projection` 正常到达 Mac；两个不同 session 都在 16–17ms 内返回 `projection.hydrate_failed`，不是 iOS timeout/decode，也不是通用 SSV2 故障。
2. 两个失败 session 的 1.18.18 `GET /session/:id/message` 均为 4 行、各含 2 个非空 reasoning part；结构固定为 `{id,sessionID,messageID,type:"reasoning",text,time:{start,end}}`，无 malformed row。
3. `agent/opencode-web/history.go` 遇到非空 reasoning 明确返回 `errUnsupportedReasoning`，这正是 hydrate 唯一命中的显式失败分支。
4. 官方 Web `server-session.ts` 仅跳过 `patch/step-start/step-finish`，保留 reasoning；CordCode 的 `RichHistoryEntry → reasoning_delta → Projection reasoning part → iOS` 已有完整 canonical 通路。

根因是 **E2 取证结论越界**：失败的 synthetic live capture 只能阻止猜测 direct-SSE reasoning，不能否定真实 HTTP history 已存在的 populated reasoning。canonical 已由设计 owner 修正，E2b sanitized 结构样本已入库。

## 1. 红灯与实现

产品代码修改前先新增并在旧实现确认红：

- 回放 E2b 的完整 user/assistant + `step-start/reasoning/text/step-finish` 顺序，调用 `GetRichSessionHistory` 必须复现 `errUnsupportedReasoning`；
- 同一 fixture 穿过 `get_session_projection` 私有 hydrate，必须复现 `projection.hydrate_failed`；
- 证明失败与 reasoning 的存在一一对应，不得用删 part、空 text 或仅断言 mapper helper 冒充 full path。

实现边界：

- `mapRichHistoryEntry` 将非空 reasoning 映射为 `Parts` 中 `{type:"reasoning", content:<exact text>}`，保持服务端 part 顺序；不得并入 `Content`、丢弃、截断或造 ID。
- `step-start`/`step-finish` 按官方 Web skip list 不进入 Projection；不要借本轮改成通用“所有未知 part 均可忽略”。
- 仍由现有 private Kernel hydrate transaction 和唯一 Kernel ingest 写入；不得新增 reducer、history fallback、raw history iOS writer、相似度去重或协议字段。
- **不要顺手实现 live reasoning**：E2 direct-SSE delta/update 顺序仍未取样，现有 live capability/advertisement 保持缺席。若本轮观察到真实 live 样本，只归档并另报，不得在同一补丁猜着接线。

## 2. Owning tests 与回归

必须至少证明：

1. E2b adapter mapping 保留 reasoning/text 的先后与精确正文；缺失/非 string `text` fail closed，空白 reasoning 的处理与官方 store 一致且有负向测试。
2. full path `get_session_projection` 成功，snapshot 中每个 assistant turn 的 reasoning 恰好一次，head/revision 只由同一 Kernel transaction 推进。
3. `step-start`/`step-finish` 零 Projection part；reasoning 不污染 answer text。
4. A1/A2/A4/A5/A6/A7/A8/A9 history、question lifecycle、C4 single-ingest、pending-live/source-fence 回归保持。
5. live reasoning 仍不广告；direct live mapper不得因本轮历史修复被暗中启用。

有界命令：

```bash
go test ./agent/opencode-web -run 'Reasoning|RichSessionHistory|History' -count=1 -timeout 3m
go test ./go-bridge -run 'OpenCodeWeb|Projection|Hydrate|Audit01' -count=1 -timeout 5m
go test ./... -count=1 -timeout 10m
go test -race ./agent/opencode-web ./go-bridge -run 'Reasoning|Projection|Hydrate' -count=1 -timeout 5m
go vet ./agent/opencode-web ./go-bridge ./core
go build ./...
python3 agent/opencode-web/testdata/official-1.18.18/harness/check_canonical_execution_design.py --self-test
```

禁止用全量 iOS 测试或 UI automation；本轮无 iOS 产品代码，默认不重装 iOS。

## 3. 产品验收前置与停止线

- Mac Release 重建、覆盖安装，确认 8777 来自 `/Applications/CordCodeLink.app`；只按既有安装纪律处理 app 自管 4096，harness 不得写 owner session。
- 安装后用 bridge/RPC 只读方式对本次两个失败 shape 做 projection preflight：都必须从 `hydrate_failed` 变为成功，且 reasoning 每 part 恰好一次；报告只写结构计数，不写 owner 正文、路径、凭据或 session ID。
- 回收本轮 4398/4399/测试进程，禁止按名称全局杀。
- 更新完成报告为“directive-014 待监工审计”；提交一次集中报告后停止。不得自行执行 owner UI、不得把 exec-plan proven 写成 supervisor verified 或 owner done。

## 4. 硬停止条件

出现任一项立即停止并报告证据，不写猜测补丁：

- E2b fixture 不能在旧实现稳定复现相同 hydrate failure；
- 真实失败 session 不含 populated reasoning，或修复后仍 `hydrate_failed`；
- 修复需要第二 Kernel writer、history fallback、协议变更或 iOS writer；
- 想实施 direct-SSE reasoning，但仍无同版本真实 live sample。
