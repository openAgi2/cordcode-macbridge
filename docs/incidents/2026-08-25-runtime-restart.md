# 2026-08-25 运行态验证事故档案（killall 匹配不到内嵌 runtime）

> 2026-09-19 从 CLAUDE.md「部署后运行态验证（2026-08-25 事故）」外置的完整事故叙述。
> 沉淀的 5 条核验规则仍逐字常驻 CLAUDE.md 同名节；本文保存「为什么」。部署/重启
> MacBridge runtime 后判定「修复无效」或「部署成功」之前先读本文。

## 事故经过

替换包 ≠ 部署完成。磁盘产物（`-version`、codesign、zip、strings）只能证明"新包的
内容"，不能证明"新包正在运行"。2026-08-25 codex-web 审批修复两次"部署成功"实际
都在跑早前启动的旧 runtime：`killall CordCodeLink` 只按主 app 进程名匹配，内嵌
runtime 是独立进程 `cordcode-bridge-runtime`（常驻 PPID=1 daemon），killall 匹配
不到它；`cp -R` 覆盖正在运行的程序文件在 macOS 上会成功，但旧进程继续占 8777
端口，新 runtime 起不来。由此把"修复无效"当作结论、把独立测试进程的实验当作
"生产路径已验证"，造成多轮误判。
