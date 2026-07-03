# Web Renderer 共享包实施文档 — 评审报告

- **评审日期**：2026-07-03
- **被评审文档**：[docs/2026-07-03-web-renderer-shared-package-implementation-plan.md](2026-07-03-web-renderer-shared-package-implementation-plan.md)
- **评审者**：评审 agent（以代码为唯一真相源）
- **核验范围**：相邻 iOS 仓库 `../cordcode-ios` 的 `message-web` 与 `remote-web`
- **方法**：读文档 → 对照代码核验（组件 diff、React/markdown 版本、package.json scripts、native bridge 接口、DiffViewer/ToolBlock 真实依赖与 action 调用、workspace 配置）→ 评估可施工性。**本评审核验"计划可施工性"，非施工结果。**
- **关联**：被评审文档对应 [执行计划](2026-07-03-architecture-health-execution-plan.md) 的批次 C，是 C 的施工前细化。

---

## 总体判断

**可施工性高，但有 2 个 P1 必须施工前补**——否则开发 agent 会在"共享包引用机制"和"host adapter 接口不完整"两处卡住或被迫自行决策。技术主张核实度高（**0 处硬事实错误**），范围/边界/验收/退出条件设计良好。

补完 2 个 P1 + 1 个 P2 标注后，可放行进入 C1。

---

## 一、技术主张核实（文档 vs 代码）

| 文档主张 | 代码真相 | 核实 |
|---|---|---|
| ToolBlock / DiffViewer 两边字节全等 | `diff` exit 0（ToolBlock 各 444 行、DiffViewer 全等） | ✅ |
| NarrativeBlock：message-web 额外有 git directive summary | message-web 版含 `extractGitDirectives` / `groupGitDirectives` / `repoName`，remote-web 无 | ✅ |
| message-web React 18.3.1、remote-web 19.2.7 | package.json: `react ^18.3.1` / `^19.2.7` | ✅ |
| message-web `build:ios` 复制进 iOS bundle | `scripts.build:ios` = `npm run build && node scripts/copy-to-ios.mjs` | ✅ |
| 验收命令 typecheck/test:vitest/build:ios/build 全部真实 | 两边 package.json scripts 全部存在 | ✅ |
| DiffViewer/ToolBlock peerDep 只需 react/react-dom | DiffViewer imports：`react`/`diffParse`/`postToNative`；ToolBlock imports：`postToNative`/`diffStats`/`DiffViewer`/`types`——**均不依赖 react-markdown/remark-gfm/react-virtuoso** | ✅ 文档 peerDeps 准确 |
| 两边 postToNative 签名一致（WebEvent） | message-web 与 remote-web 均 `postToNative(event: WebEvent)`；remote-web 另有 `setOutboundSink` 注入式 outbound | ✅ host adapter 方向可行 |
| remote-web `base: "/web/"` 面向 Relay VPS | 未独立核验 vite config，但 `build` script 真实 | ⚠️ 未深核，build 验收会兜底 |

**0 处硬事实错误**。文档第 156 行"测试空跑护栏"、第 6 节禁止清单、第 7 节退出条件均设计良好。

---

## 二、必须修正（P1，施工前补）

### P1-1：共享包的引用机制未指定（最大遗漏）

- **现状**：cordcode-ios 根目录**不是 monorepo workspace**（无根 `package.json` / `pnpm-workspace.yaml` / `turbo.json` / `nx.json`）。文档第 3 节说 shared package "作为源码包被两个 web app 直接引用，第一轮不发布 npm 包"，但**全文未指定具体 wire 机制**（grep `workspace`/`file:`/`npm link`/`tsconfig paths`/`resolve.alias` 均 0 命中）。
- **风险**：开发 agent 会在多种机制间自行决策，且部分选择不可复现或污染 lockfile：
  - `file:../shared-message-renderer`：copy 进 node_modules，改共享包要重 install，lockfile 要提交
  - `npm link`：开发期可用，**不可复现**，CI 会断
  - **tsconfig paths + vite resolve alias**：源码级共享，最轻量，不污染 lockfile，改即生效，但要在两边 `tsconfig.json` + `vite.config` 各加配置
  - 根 `package.json` workspaces：要新增根 package.json + workspaces 字段，改动根目录布局
- **修正**：文档第 3 节钉死一种。**推荐 tsconfig paths + vite resolve alias 源码级共享**（最轻量、可复现、不污染 lockfile、契合"源码包不发布 npm"定位）；并在 C1 验收补"两个 app 的 tsconfig.json + vite.config alias 指向 `../shared-message-renderer/src`，typecheck + build 通过"。

### P1-2：RendererHostAction 只定义 openDetail，但 ToolBlock 实际还用 permissionAction + questionAction

- **现状**：文档第 4 节 `RendererHostAction` union 只列 `openDetail`。但核验 ToolBlock 的 4 处 `postToNative` 调用，type 分别是：
  - 行 297：`openDetail`（kind: 'tool'）
  - 行 395：`permissionAction`
  - 行 417 / 432：`questionAction`
- 即 ToolBlock 第一轮迁移（C3）必须经 host adapter 承载 **3 种 action**，而接口只定义了 1 种。
- **风险**：C3 迁移 ToolBlock 时要么被迫临时扩接口（违反"第一轮不改行为"），要么 ToolBlock 不能完全迁移（wrapper 里继续直调 `postToNative`，破坏 host adapter 单一通道目标）。
- **修正**：第 4 节 `RendererHostAction` 补 `permissionAction` 与 `questionAction` 两个 variant（payload 按 `message-web/src/types.ts:230/237` 现有形状），让接口第一轮就覆盖 ToolBlock 全部调用。DiffViewer 只用 `openDetail`，C2 不受影响。

---

## 三、建议改进（P2，不阻塞）

### P2-1：第 4 节 ReasoningBlock labels 注入应标注"仅为未来设计示意"
文档第 116-121 行给了 ReasoningBlock 的 `labels` 注入示例（中/英文案）。但第一轮**不迁移 ReasoningBlock**（C1-C3 只迁 DiffViewer/ToolBlock）。建议在该段开头加一句"以下 labels 设计为后续迁移 ReasoningBlock 时使用，本轮不实现"，避免开发 agent 误以为本轮要在 host adapter 实现 `labels` 字段。

---

## 四、已自带的好护栏（值得肯定）

- **第 6 节禁止清单**明确：不迁移 git directive summary、不迁移 relay/pairing、不引入 fallback/mock、不改 snapshot envelope / bridge revision、不混入 React 升级、不跑 UI automation。
- **第 156 行测试空跑护栏**：`npm run test:vitest -- DiffViewer` 若无匹配测试不得声称通过——这是上次 capability 评审 P1-1 的同类风险，文档已自己 cover。
- **第 7 节退出条件**覆盖：React 18/19 类型不兼容、DOM className/test id 变化、build 失败根因、需 UI automation。
- **第 9 节进入条件**要求"本文经 review" + "install 状态可复现" + "已读 iOS CLAUDE.md"。

---

## 五、文档第 9 节进入条件 — 当前状态回填

| # | 进入条件 | 当前状态 |
|---|---|---|
| 1 | 本文经 review，确认第一轮只迁 DiffViewer + ToolBlock | 本次评审进行中；范围 ✅，但 P1-1 / P1-2 补完才算"review 通过" |
| 2 | message-web / remote-web package install 状态可复现 | 待施工 agent 核验（依赖 P1-1 引用机制确定后的 lockfile） |
| 3 | 执行 agent 已读 `../cordcode-ios/CLAUDE.md` | 待施工 agent 确认 |
| 4 | 明确本轮不做 UI automation | 文档第 1 / 6 / 7 节均已明确 ✅ |

---

## 六、小结

技术主张核实度高（0 硬事实错误）、范围与边界设计良好，**但 P1-1（引用机制）与 P1-2（host adapter 漏 permissionAction / questionAction）必须在交给开发 agent 前补齐**——前者不补开发 agent 会自行决策导致不可复现，后者不补 C3 迁移 ToolBlock 会卡住。补完 2 个 P1 + P2-1 标注后，可放行进入 C1。

施工完成后建议另起一轮**结果评审**（按第 8 节完成证据清单 + 跑第 5 节验收命令）。

**未修改任何代码。**
