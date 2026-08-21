# testdata 来源声明

- 全部 fixture 由 Phase 0 采集（隔离 CODEX_HOME + 本地 mock Responses provider 上游，
  app-server 为真实官方二进制 codex-cli 0.149.0-alpha.4），原始证据与采集器见
  `scripts/codex-web-phase0/`（schemas 完整 bundle、dumps 全量、validate 脚本）。
- 本目录为 contract test 消费子集：dumps/*（raw.jsonl + meta.json）与两个 v2 schema 聚合 bundle。
- 元数据、脱敏说明与样本裁决见 README.md；设计回写见 docs/2026-08-21-codex-web-backend-design.md §22。
