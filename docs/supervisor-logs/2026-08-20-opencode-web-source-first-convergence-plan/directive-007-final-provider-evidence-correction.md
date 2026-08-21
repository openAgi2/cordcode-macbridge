# 监工指令 7 号：Final provider evidence correction（E1b/E4b/E5b）

> Canonical：`docs/2026-08-20-opencode-web-source-first-convergence-plan.md`（commit `8d110f7`）  
> 范围：仅一次联合 provider/config/variant 真实取证  
> 状态：待开发 agent 执行  
> 产品代码：继续冻结

## 1. 为什么还有这一轮

Directive-006 的主体是有效的，WP、E2、E3、E6、E7 已进入 canonical 决策。但独立审计发现 C5/variant 仍缺同一组物理输入：

- E1 证明 top-level `variant` 能提交并持久化，却没有非空 `/provider...models[modelID].variants` 目录，无法设计选择器；
- E4 只有 sanitized provider payload，没有指令要求的 raw+sanitized provenance pair；
- E5 没有捕获 official Web 实际读取的 `GET /config` 响应，并且单模型目录无法区分 `/provider.default` 与 first-model fallback。

这三个缺口共享同一个 provider sandbox，本指令要求一次联合采完。完成后不再逐功能取证；设计 owner做一次最终 mapping sync，然后下发 C2–C7 集中实施。

## 2. 固定环境与安全边界

- pinned OpenCode `1.18.18` / source `2cba7e227d`；隔离 HOME/XDG/workspace；4398/4399；4096 owner listener 不写、不停、不重启。
- deterministic local provider 必须有**至少两个 model**，且默认 model 不能恰好等于稳定排序后的 first model。
- fixture credential 必须使用明确的非秘密 sentinel（例如 `fixture-not-a-secret`）。raw 可以保存该 sentinel，但不得保存真实 token/account/path/auth header。
- raw→sanitized 规则必须可机械验证：只替换声明的 value classes，key/type/list order/model IDs/default relation 不变。
- 每个命令带超时；只回收本任务进程；结束时 4398/4399 空闲、4096 PID 前后不变。

## 3. E4b：provider raw provenance

捕获真实 `GET /provider` request/status/response 的 raw + sanitized 对：

1. top-level 必须由 checker 从 raw 推导 `{all,default,connected}`；
2. provider row/model row 的全部 key/type/order从 raw 推导；
3. sanitized 与 raw 的结构递归相同，只有 allowlisted value classes 可不同；
4. 篡改 raw key/type/order/default/connected/model ID 或 sanitized 非允许字段必须 FAIL；
5. `env/options` 与 credential 值仍只作 opaque evidence，不作产品 mapping。

不要用“raw 因规则拒收”再次绕开；本轮配置的 sentinel 就是为安全归档 raw 设计的。

## 4. E5b：official configured/default/fallback inputs

对 valid / invalid / absent 三个隔离配置分别捕获：

- official v1 `GET /config` 的 request/status/response，确切 `model` 字段存在/值/缺失；
- 同一模式下的 `GET /provider`；
- official picker所需的 connected providers、provider default、两个 model 的稳定目录；
- 选择结果对应的 prompt request和 persisted user/session model。

Checker 必须独立证明：

1. valid configured model 存在且属于 connected catalog；
2. invalid configured model不属于 catalog，不能作为有效选择；
3. absent config确实缺 `model`，不是空字符串/summary；
4. `/provider.default[providerID]` 与 stable first model 是不同 ID；
5. official source `resolveDefaultModel` + `prompt-model-selection.ts` 的 candidate order可用这些 raw inputs逐支计算；
6. 任何篡改 `/config.model`、connected、default 或 model rows 都会改变/破坏 derived result。

如果 v1 official UI 实际请求的配置 route 不是 `/config`，以 source + live request 为准，并在 inventory/canonical correction report中写出真实 route；不得靠配置文件内容代替 HTTP response。

## 5. E1b：non-empty catalog variant

使用 official 支持的 provider/model configuration 让至少一个 live model 的 `variants` 对象非空：

1. raw `/provider` 证明 model `variants` 的确切 key/value shape；
2. official Web选择其中一个真实 key；
3. 捕获 resulting `prompt_async` top-level variant；
4. reload证明同一 user message `info.model.variant`；
5. unset control不带 variant；未列出的 variant 不得被当作可选项。

如果查阅 pinned source并尝试受支持配置后，1.18.18 local deterministic provider无法产生非空 variants：记录 source、配置、请求与失败，标 E1b `BLOCKED/UNSUPPORTED`。不要修改 official source、手写 provider响应或用 product fake server造非空目录。

## 6. 允许/禁止与完成报告

允许修改：harness、samples、evidence checker、sample inventory、exec-plan evidence状态和本指令报告。

禁止修改：`agent/opencode-web` 产品 Go 文件、`go-bridge`、`core`、Mac app、iOS、`docs/protocol`、WireDescriptor/capability，以及任何 fallback/translator/UI/build/install。

完成报告一次性交付：

1. E1b/E4b/E5b captured/blocked/missing；
2. source file:line、raw/sanitized路径和 derived physical facts；
3. checker + destructive self-test真实输出；
4. raw/sanitized结构等价与 leak scan；
5. 4398/4399/4096前后证明；
6. commit/file list、工作树干净、零产品/协议/iOS改动声明。

报告后停止。不得自行更新 canonical mapping、进入 C2 decoder/C3–C7、改协议、激活 capability 或安装产品。
