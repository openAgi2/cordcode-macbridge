# CordCode Engineering Constitution

日期：2026-07-03

本文件把多 agent / 多模型开发中最容易漂移的工程约束写成仓库规则。它不是一次性清债清单，而是后续改动的默认边界：先让规则可见，再逐步把存量问题收敛。

## 原则

1. 真实路径失败必须暴露真实错误，不用 fallback、mock、placeholder 或缓存快照伪造成功。
2. wire protocol 字面量是冻结契约；修改 protocol、pairing、relay、capability 或 connection state 时，必须同步 Mac protocol pack、iOS mirror、定向测试与活文档。
3. UI automation、snapshot tests、simulator automation、真机安装只在 owner 明确授权时运行。
4. 新增工程规则先 warning-only；只有存量债务归零或有基线机制后，才能改成 required gate。

## 日志

Swift 日志应逐步收敛到明确边界：

- 产品运行态优先使用统一 wrapper 或 `os.Logger`。
- `NSLog` 只保留在启动期、崩溃排查、跨进程 bootstrap 等需要系统日志可见性的边界。
- `print` 只允许在临时本地诊断、测试或 debug-only 代码中出现；提交前应删除或解释。

Go 运行态优先使用 `slog`，避免新增散落的 `fmt.Println` / `log.Printf` 调试输出。

## 本地化

新增用户可见文案必须走现有本地化路径。不得在 Swift UI 或 Web UI 里新增硬编码中文/英文用户可见字符串，除非该文件已经是测试 fixture、内部日志或协议常量。

## 测试注入

生产类中的 `ForTesting` 是技术债，不是默认注入方式。新增测试注入优先使用协议、工厂或构造参数。确需 `ForTesting` 时，必须满足：

- 标注作用范围；
- 不改变生产默认路径；
- 有后续移除条件或说明为什么协议注入不适合。

## 长文件与长方法

长文件和长方法先 warning，不硬 fail。新增大块逻辑时优先拆到已有职责边界：

- Go wire/RPC 适配留在 `go-bridge/`，agent 行为留在 `agent/*` / `core/`。
- Swift 连接策略、recovery、transport、UI state 不应继续堆进同一个 god-object。
- React renderer 的纯展示组件应优先向共享包收敛，避免 message-web / remote-web 双写。

## 非阻塞检查

本轮新增 `scripts/check-architecture-hygiene.sh`。它只输出当前存量计数和规则提示，默认 exit 0；它的职责是让漂移可见，不在存量问题尚未清掉时阻塞 CI。
