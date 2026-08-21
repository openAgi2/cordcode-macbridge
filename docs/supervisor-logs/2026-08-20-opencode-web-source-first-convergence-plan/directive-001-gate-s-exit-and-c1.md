# 监工指令 1 号：Gate S 正式退出并实施 C1

> 本文件补录 2026-08-20 已在 owner 对话中下发的指令；补录不改变原指令范围。

## 范围

1. 先以独立 docs/checker/exec-plan/handoff commit 正式退出 Gate S；退出前 Gate B、S1/S2、S3、S4 checker 与 self-test 必须全绿。
2. Gate S 退出提交成功后，单独实施 C1 version/transport boundary；不得夹带 C2–C7。
3. OpenCode 1.18.18/generation118 是唯一 verified product adapter；v2/unknown fail closed/quarantine，零 prompt、零 SSE ingest、零 Kernel、零 capability。
4. 删除或封死 unknown-shape recursive fallback，保留 Basic Auth、directory scoping、HTTP timeout、SSE no-lifetime timeout 与 bounded reconnect。
5. transport/probe/reconnect 只属于 control-plane，不得成为 timeline writer。
6. 使用官方 checkout `2cba7e227d` 与 A1/A5 同版本样本；若出现未覆盖的新翻译语义，先补真实样本。

## 验收

- 完成并证明 `c1-version-transport-{impl,tests,regression}`。
- 定向测试覆盖 verified 1.18.18、v2 quarantine、零 prompt/Kernel、transport-only 与错误分类。
- 不修改 protocol、WireDescriptor、capability advertisement 或 iOS writer。
- Gate S exit 与 C1 为两个独立 commit；C1 后停止，不进入 C2。

