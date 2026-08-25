# catalog-live 样本密钥抹除与历史重写记录（2026-08-26）

## 事件

- 触发：owner 指令推送 codex-web 合并至 GitHub main，被 **GitHub Push Protection** 拦截。
- 事实：`agent/codex-web/testdata/official-0.149.0-alpha.4/dumps/catalog-live/raw.jsonl:1` 含一个 40 字符 `ghp_` PAT（引入提交 `bd6dc29`，脱敏流程漏网）；全仓仅此一处；不在本机 `~/.codex` 配置中；**远端从未收到**（推送被整体拒绝，origin/main 保持 9a77ae6）。
- owner 裁决（2026-08-26）：本地抹除 + 历史重写（方案 A），不走 GitHub 解锁后门。

## 处置

- 占位符：`ghp_REDACTED_FOR_SANITIZED_FIXTURE`（含下划线，不再匹配 GitHub token 模式）。
- 重写：`bd6dc29` amend 抹除后 rebase 重放其全部 86 个后继提交（纯线性、零冲突）；`main` 与 `codex/codex-web-backend` 同步归位新尖端。
- 旧对象仅存于本地 reflog（机器内，从未外泄）；如需彻底清除本地残留：`git reflog expire --expire=now --all && git gc --prune=now`（会同时清掉其他安全 reflog，慎用）。
- 证据链维护：完成情况/监工日志中引用的旧哈希按下表映射到新哈希（作者/日期/内容不变，仅哈希变化）。
- 备份：`~/cordcode-git-backups-20260826/macbridge-all-pre.bundle`（含密钥）已删除并以重写后全量 bundle 替换。
- Follow-up（待开发 agent）：`scripts/codex-web-phase0/` 的脱敏器需补 `ghp_`/`github_pat_` 模式清洗，防再次漏网。

## 旧→新哈希映射（87 对，时间序）

```
bd6dc29 -> dea5d1b  # redacted base (fix: decode thread/list...)
f000da0 -> 31389aa
5fea386 -> 02753bb
1cc075d -> c634c7c
eb76583 -> 3e05a83
39aac41 -> 883d7ec
37408cb -> ad5d78b
b4325c5 -> b7fcbcd
047f2ff -> 2cdfd1e
a867f56 -> 77e9d51
4a478ca -> 8671d36
247191a -> a8ce31e
f8a66aa -> c7a5650
d3fba22 -> 5afd09e
387f75f -> eec322d
568fed6 -> 72c5ce0
9cf9287 -> 4d9d63f
72ce7e0 -> 9630093
5910f65 -> 446b94c
e87be32 -> fd6c621
1846600 -> 4a8c646
3874859 -> 8f0ff5a
60c5b19 -> a7877ee
b45fa16 -> 794ec27
d132a0f -> 717f653
4367b5e -> 48a314b
169d668 -> 69dd99c
63ddf57 -> 0967a8e
d9d2a32 -> a3a11b1
4f56738 -> c084305
38becfb -> 3f51c95
a80b724 -> a03adec
842a02c -> 8f4c324
ea957b1 -> ca28c54
8648b9b -> 629389e
afcea3d -> 90ef178
f0fa203 -> 80d0248
7e2348d -> 2ac7653
935fe38 -> 9e7ffe5
c7246bd -> 990c750
758e64f -> 30eb1ef
bd80799 -> 7d5dd15
ddbbc5e -> 301d45c
f2fc4bb -> 27f7511
b088031 -> 38ddc15
33ca4bc -> 3bc2620
186dd64 -> 0304b5e
de1db53 -> 4939bcf
ddfba0a -> 2a62a27
1ae92f6 -> 2e7516f
b2a3e0d -> 2b8c8ff
5d28346 -> c74b37f
702c5d3 -> 702dc08
480a39b -> b070f64
6a305a8 -> 500fd1a
089822c -> d032396
b70009d -> 8510af2
10548da -> e564e3c
6f765fc -> 69b004f
0f524d7 -> 27797fd
0419729 -> 2c1435a
feb3ada -> f7fabc0
09bd063 -> 02430b6
0267b71 -> 4446a41
f354fc6 -> f660065
e21a6a3 -> f53ed10
89954bc -> d436a43
202b41c -> 4b32402
92ee2b5 -> a6c18f9
980d358 -> 0a85ad5
145be8c -> cf7866d
cef3d69 -> 904b6cd
3d84b28 -> a37735f
e319ea5 -> 8bdd407
8addcb1 -> cbf076d
cde3cb0 -> 2e20c11
faa9e92 -> 044d9bf
27e5f50 -> a30be18
ffcbb81 -> cb6eed4
d18c6ec -> 95db883
6b155ae -> e68ac5a
5c74f1c -> fcee883
2063387 -> ca23b45
fee24fe -> c6fa9b8
cff96ff -> 669dc62
8d51ccb -> abf16dd
c71b5dd -> de25cfa
```
