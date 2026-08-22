# Codex Web requestUserInput daemon-WS Gate

- 日期：2026-08-22
- 官方二进制：`codex-cli 0.149.0-alpha.4`
- 结论：**PASS**（首次失败根因已定位为非法测试 tool 输入；修正后 daemon WS 三题全链通过）

## 隔离条件

- `CODEX_HOME=/tmp/cw-e2e-home-<pid>`，测试结束删除；所有 daemon stop 均显式携带该 home。
- 上游为 `scripts/codex-web-phase0/mock_provider.py`，只控制 Responses API 模型输出；app-server JSON-RPC 为官方二进制真实路径。
- `thread/start.config.features.default_mode_request_user_input=true`；官方 warning 通知确认 feature 已启用。
- initialize `capabilities.experimentalApi=true`。
- prompt：`MOCK:ASK3 multi question`；provider 首轮真实产生 `request_user_input` function call（三题）。

## 首次失败与根因

旧 `MOCK:ASK3` 的第二题没有 options。官方源码
`codex-rs/core/src/tools/handlers/request_user_input_spec.rs` 的
`normalize_request_user_input_tool_args` 明确要求每题 non-empty options，并将 `is_other=true`
统一加入自由文本 Other。provider 第二轮收到的真实 function-call output 为：

```text
request_user_input requires non-empty options for every question
```

因此首次运行没有产生 server request 是合法的 tool 输入拒绝，不是 daemon transport 丢帧。

## 修正后实测结果

运行：

```text
CODEXWEB_E2E=1 go test ./agent/codex-web \
  -run '^TestE2EInteractionUserInput$' -count=1 -v
```

将第二题修正为两个合法 options 后得到：

1. 当前 daemon WS 连接收到真实 `item/tool/requestUserInput`，包含三题与官方 request id；
2. adapter 把 option / 自由文本 Other / option 映射为一次官方 answers map；
3. 收到 `serverRequest/resolved`；
4. turn 最终 `completed`。

对照：`scripts/codex-web-phase0/dumps/interaction/raw.jsonl` 保留 stdio 单题样本；本次新增 daemon WS
三题回归，transport 两条路径分别有真实证据。

## 判定与解除条件

- `p4-userinput-impl/tests/regression` 均可 proof-carrying 完成；
- capability 仍按 experimental/version gate 开启；
- mock provider 的问题必须符合官方 tool schema，每题保留 2–3 options；自由文本由官方 Other 路径表达；
- 禁止本地合成 request、假 request id、自动答案或 fallback。
