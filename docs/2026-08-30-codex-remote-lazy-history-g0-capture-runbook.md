# G0 live 采集 runbook（owner 操作手册）

懒加载计划 Phase 0 的 live fixture 采集需要 owner 在场完成两步授权。探针只读取线程历史
元数据与结构，**不需要您发送任何聊天消息**。全程约 3–8 分钟（视线程数量与深度）。

## 前置条件

- ChatGPT Desktop 正在运行（当前 ✓）；
- owner 在 Mac 前可完成浏览器授权；
- 配对码（若需要）只输入探针弹出的 localhost 页面表单，**绝不进聊天/终端/磁盘**。

## 采集步骤

1. **owner 发起授权指令后**，执行方在 Mac 仓库根目录运行：

   ```bash
   node agent/codex-remote/probe/history_probe.mjs 2>probe-events.log | tee /tmp/g0-fixture.out
   ```

2. **浏览器 step-up**：探针会打开浏览器授权页 → owner 点击授权（10 分钟窗口）。

3. **配对码（仅当探针提示 `pairing_input_opened`）**：ChatGPT Desktop → 设置 →
   控制这台 Mac → 切到"电脑"标签 → 刷新生成新配对码 → 输入探针打开的
   `http://localhost:1455/pair?...`（或 1457）表单 → Pair。

4. 等待探针完成（进度事件在 stderr：`history_inventory` → `discovery` →
   `chain|summary|desc` 翻页 → `items|*` 采样 → `t0.6-candidate-resume` →
   `revoke`）。看到 stdout 出现 `REDACTED_FIXTURE={...}` 即完成。

## 采集后处理（执行方完成，owner 只需知晓）

```bash
# 1) 保存 fixture（编号顺延当前 attempt-008）
python3 - <<'PY'
line = next(l for l in open('/tmp/g0-fixture.out') if l.startswith('REDACTED_FIXTURE='))
import json
fixture = json.loads(line[len('REDACTED_FIXTURE='):])
# owner 补一句 attestation 后由执行方填入 operator_attestation
json.dump(fixture, open('agent/codex-remote/testdata/phase0/live/attempt-009-history-lazy-g0.json','w'), ensure_ascii=False, indent=1)
PY

# 2) secret scan（必须 PASS）
gitleaks dir --redact --config .gitleaks.toml agent/codex-remote/testdata/phase0/live/

# 3) 结构校验（history-only 模式：脱敏策略扫描 + history fixture 结构）
node agent/codex-remote/validate/controller-fixtures.mjs --require-live --history-only

# 4) §3.0.7 负结果断言 + §3.0.5 九项清单
node agent/codex-remote/validate/history-fixture-assert.mjs \
  --fixture agent/codex-remote/testdata/phase0/live/attempt-009-history-lazy-g0.json
```

## 需要 owner 裁决的事项（采集完成后一并处理）

1. **目标版本重锚定（§7-4 版本偏差）**：Desktop 已自动升级到 `26.825.41651`
   （bundle 7345）/ 内嵌 `codex-cli 0.151.0-alpha.7.1`，计划冻结基线是
   `26.825.32147` / `0.150.0-alpha.12.2`。上游 diff（→ rust-v0.151.0-alpha.7.2）
   对五个锚点文件为**纯增量**（新增 `usageMetadata`、`functionCallOutput` item
   变体、misalignment 错误细节；锚点结构体/函数未动）。建议：G0 fixture 按
   实测版本（alpha.7.1）取证并作为目标基线，源码锚点继续引用
   `rust-v0.150.0-alpha.12.2`（差异点已记录）。需要 owner 明确接受。
2. **T0.5 legacy 裁决**（仅当 inventory 中出现 `historyMode=legacy` 线程）。
3. **G0.5 Reasoning content**：若实测无任何非空 `content[]` 样本，需裁决从验收
   主张中删除"完整思考"表述。
4. **资源门裁决值**：maxPages/maxBytes/timeout 由 T0.2 资源画像数据提出，owner 确认。
5. **T0.6 resume 候选**：默认维持 baseline；三条件齐备时才由 owner 裁决启用。

## 安全边界（探针内建，owner 无需操作）

- 独立探针密钥（login-Keychain 不可导出），凭据只在内存；
- 只允许 chatgpt.com 官方 origin 与冻结路径；
- 409/单owner 冲突时停止，绝不断开/吊销其他 controller（含官方 iOS controller）；
- 结束时自动只吊销探针自己的 controller 并验证吊销生效、删除探针密钥；
- 输出只含结构字段、计数、长度、枚举与稳定假名（id-N / cur-N），无任何用户内容。
