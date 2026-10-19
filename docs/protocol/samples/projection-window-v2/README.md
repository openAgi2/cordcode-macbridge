# projection-window-v2 冻结样本（S-3 v2 summary，2026-10-19）

冻结依据：`docs/2026-10-19-s3-v2-summary-freeze-spec.md`（v3，评审通过）+
`docs/protocol/bridge-v1.md` §Projection Window v2 Summary。

| 文件 | SHA-256 |
| --- | --- |
| `window-summary-spec.json` | dfacf199902fb307fd659db80149a6e9d39c7e9ecb048f840af719f0470e085d |
| `anchor-newer-summary-response-spec.json` | 8959e1a641c40b74d7a23a63d23b39ac57c1f4ea14f1791d7f48182710f39182 |

两仓字节一致副本纪律：iOS 仓同路径副本必须与本目录文件逐字节一致；改任一冻结文件
必须同步另一仓副本与本 README 哈希，并同步更新两仓 spec 测试冻结断言。

`window-summary-spec.json` 含 always-visible 例外通道双实例（permission 族 part
`turn-104`、user_input pending part `turn-105`，均原样内嵌、无新 wire 字段）。
