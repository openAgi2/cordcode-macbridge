# Web Renderer 共享包设计实施文档

- **日期**：2026-07-03
- **对应执行队列**：`batch-c-web-renderer-shared-package-impl`
- **范围**：相邻 iOS 仓库 `../cordcode-ios` 的 `message-web` 与 `remote-web`
- **性质**：进入 batch C 前的施工设计。本文不直接代表已实施。

## 1. 目标

把 `message-web` 与 `remote-web` 中已经重复的消息渲染组件收敛到一个共享包，降低后续 renderer 改动双写和漂移风险。

第一轮只做低风险共享：

1. 稳定数据类型；
2. 无平台状态所有权的 block 组件；
3. 通过 host adapter 注入宿主能力，不在共享包里直接依赖 iOS WebKit bridge 或 remote-web relay 状态。

第一轮不做：

- 不迁移 app 入口、store、transport、WebKit bridge、relay/pairing、git directive UI；
- 不统一 React 18/19 版本；
- 不修改 Swift/iOS 协议模型；
- 不运行 UI tests、snapshot tests、simulator automation 或真机 UI 操作。

## 2. 当前证据

代码位置：

```text
../cordcode-ios/message-web/src/
../cordcode-ios/remote-web/src/renderer/
```

已核对的重复情况：

- `ToolBlock.tsx`：两边字节一致；
- `DiffViewer.tsx`：两边字节一致；
- `ReasoningBlock.tsx`：仅文案不同，`message-web` 为中文，`remote-web` 为英文；
- `NarrativeBlock.tsx`：存在产品差异，`message-web` 额外解析并展示 git directive summary；
- `ProcessGroup.tsx`：高度重复，但通常牵涉 turn/group 组合行为，第一轮不迁移。

包差异：

- `message-web`：React 18.3.1，`build:ios` 会把产物复制进 iOS bundle；
- `remote-web`：React 19.2.7，`base: "/web/"`，面向 Relay VPS 静态部署；
- 两边都有 `react-markdown` / `remark-gfm`，但版本不同；
- 两边 TypeScript 均为 strict + bundler module resolution。

## 3. 推荐目录

在 iOS 仓库新增：

```text
../cordcode-ios/shared-message-renderer/
  package.json
  tsconfig.json
  src/
    index.ts
    types.ts
    host.ts
    utils/
      diffParse.ts
      diffStats.ts
    components/
      blocks/
        DiffViewer.tsx
        ToolBlock.tsx
```

`shared-message-renderer` 作为源码包被两个 web app 直接引用，第一轮不发布 npm 包。

推荐 package 约束：

- `private: true`;
- `type: module`;
- `peerDependencies`: `react >=18 <20`, `react-dom >=18 <20`;
- devDependencies 使用本仓已有 TypeScript/Vitest 版本，不引入新的 renderer 框架。

### 3.1 引用机制

第一轮钉死为 **TypeScript paths + Vite resolve alias 的源码级共享**：

- 不使用 `file:` dependency；
- 不使用 `npm link`；
- 不新增 iOS 仓库根 `package.json` / workspace / turbo / pnpm workspace；
- 不发布 npm 包；
- 不把 shared package copy 进任一 app 的 `node_modules`。

两个 app 都从源码 alias 引用：

```ts
import { DiffViewer, ToolBlock } from '@cordcode/shared-message-renderer';
```

`message-web/tsconfig.json` 和 `remote-web/tsconfig.json` 都必须增加：

```json
{
  "compilerOptions": {
    "paths": {
      "@cordcode/shared-message-renderer": ["../shared-message-renderer/src/index.ts"],
      "@cordcode/shared-message-renderer/*": ["../shared-message-renderer/src/*"]
    }
  },
  "include": ["src", "../shared-message-renderer/src"]
}
```

`message-web/vite.config.ts` 和 `remote-web/vite.config.ts` 都必须增加等价 alias：

```ts
import { fileURLToPath, URL } from 'node:url';

resolve: {
  alias: {
    '@cordcode/shared-message-renderer': fileURLToPath(new URL('../shared-message-renderer/src/index.ts', import.meta.url)),
    '@cordcode/shared-message-renderer/': fileURLToPath(new URL('../shared-message-renderer/src/', import.meta.url)),
  },
},
```

这让 shared package 作为源码参与两个 app 的 typecheck/build，改动即时生效且不污染 lockfile。若 TypeScript/Vite 对 alias 解析有差异，优先修 alias，不改为 `file:` 或 `npm link`。

## 4. Host Adapter 边界

共享包不得直接 import：

```text
message-web/src/bridge/native
remote-web/src/renderer/bridge/native
remote-web/src/relay/*
OpenCodeiOS/*
```

共享包定义最小 host 接口：

```ts
export type RendererHostAction =
  | {
      type: 'openDetail';
      payload: {
        kind: 'thinking' | 'tool' | 'prompt';
        groupID?: string | null;
        stepID?: string | null;
        text?: string | null;
        title?: string | null;
      };
    }
  | {
      type: 'permissionAction';
      payload: {
        toolUseId: string;
        action: string;
      };
    }
  | {
      type: 'questionAction';
      payload: {
        questionId: string;
        optionId: string;
      };
    };

export interface RendererHost {
  post(action: RendererHostAction): void;
  labels?: Partial<RendererLabels>;
}
```

两个 app 各自提供 adapter：

- `message-web` adapter 调用现有 `postToNative`；
- `remote-web` adapter 调用 remote 现有宿主通道；
- 共享组件只调用 `host.post(...)`。

`ToolBlock` 第一轮迁移必须只通过 `host.post(...)` 发出 `openDetail`、`permissionAction` 和 `questionAction`，不得保留对任一 app `postToNative` 的直连 import。

`ReasoningBlock` 的文案差异不应硬编码在共享包里。以下 `labels` 设计仅为未来迁移 `ReasoningBlock` 时使用，**本轮不实现 `ReasoningBlock` 迁移**；若后续迁移，使用 `labels` 注入：

```ts
reasoningStreaming: '正在思考…' | 'Thinking…'
reasoningComplete: '思考过程' | 'Thought'
```

## 5. 第一轮迁移顺序

### C1. 建包但不迁移行为

1. 新增 `shared-message-renderer`；
2. 从两边当前一致代码中抽出 `types.ts` 的稳定子集；
3. 抽出 `diffParse` / `diffStats` 这类纯函数；
4. 添加共享包自身 typecheck/test/build 脚本；
5. 为 `message-web` 与 `remote-web` 配置 `tsconfig.json` paths 与 `vite.config.ts` resolve alias，指向 `../shared-message-renderer/src`；
6. 两边 typecheck/build 必须能从源码 alias 成功解析 shared package。

验收：

```bash
cd ../cordcode-ios/shared-message-renderer
npm run typecheck
npm run test
cd ../message-web && npm run typecheck && npm run build
cd ../remote-web && npm run typecheck && npm run build
```

如果 package-lock 需要更新，必须同步提交对应 lockfile；不得用本机 node_modules 状态冒充可复现构建。

### C2. 迁移 `DiffViewer`

1. 把 `DiffViewer` 移入 shared package；
2. 把 `openDetail` 从直接 `postToNative` 改为 host adapter；
3. `message-web` 和 `remote-web` 保留薄 wrapper，只负责传 host；
4. 不改 CSS className，不改 DOM 结构，不改 test id。

验收：

```bash
cd ../cordcode-ios/message-web && npm run typecheck && npm run test:vitest -- DiffViewer
cd ../cordcode-ios/remote-web && npm run typecheck && npm run test:vitest -- DiffViewer
```

若没有匹配测试文件，不得声称测试通过；应补共享包单元测试或记录测试缺口。

### C3. 迁移 `ToolBlock`

1. `ToolBlock` 移入 shared package；
2. 保持 `statusTone`、`isCommandTool`、`commandText`、fold 阈值、todo 渲染语义不变；
3. `DiffViewer` 使用 shared 版本；
4. 两边 wrapper 只注入 host 和 labels；
5. 不迁移 `NarrativeBlock` 的 git directive summary。

验收：

```bash
cd ../cordcode-ios/message-web && npm run typecheck && npm run test:vitest -- ToolBlock
cd ../cordcode-ios/remote-web && npm run typecheck && npm run test:vitest -- ToolBlock
```

### C4. 构建集成

1. `message-web` 能正常 `npm run build:ios`；
2. `remote-web` 能正常 `npm run build`；
3. 若只改 web 代码但不改 Swift，不自动跑 UI tests；
4. 若 `message-web` 产物进入 iOS bundle，按 iOS 仓库规则检查 connected physical iPhone；没有真机则记录未检测到，不造假。

验收：

```bash
cd ../cordcode-ios/message-web && npm run build:ios
cd ../cordcode-ios/remote-web && npm run build
```

## 6. 禁止事项

- 不把 `message-web` 的 git directive summary 移入 shared package；
- 不把 remote relay/pairing 逻辑移入 shared package；
- 不引入生产 fallback/mock/placeholder 来掩盖 adapter 缺失；
- 不改变 renderer snapshot envelope 或 Swift/JS bridge revision；
- 不把 React 升级/降级混进本轮；
- 不运行 UI automation 或 snapshot tests，除非 owner 当前任务明确授权。

## 7. 失败退出条件

遇到以下情况停止扩大范围：

1. shared package 需要修改 Swift/JS contract 才能编译；
2. React 18/19 类型差异导致组件签名无法稳定复用；
3. `DiffViewer` / `ToolBlock` 迁移需要改变 DOM className 或 test id；
4. `message-web` `build:ios` 或 `remote-web` `build` 任一失败且根因不是本轮可局部修复；
5. 需要 UI automation 才能证明视觉等价。

## 8. 完成证据

batch C 真正实施完成时，报告必须列出：

- shared package 文件清单；
- 两边 wrapper 文件清单；
- 未迁移组件清单与原因；
- `message-web` typecheck/test/build 结果；
- `remote-web` typecheck/test/build 结果；
- iOS 真机探测结果（仅探测/安装，不做 UI 操作）；
- 没有修改 protocol/wire 字面量的证据。

## 9. 推荐进入条件

进入 batch C 实施前先满足：

1. 本文经 review，确认第一轮只迁移 `DiffViewer` + `ToolBlock`；
2. iOS 仓库当前 `message-web` / `remote-web` 的 package install 状态可复现；
3. 执行 agent 已读 `../cordcode-ios/CLAUDE.md`；
4. 明确本轮不做 UI automation。
