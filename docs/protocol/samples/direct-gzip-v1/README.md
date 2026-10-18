# direct-gzip-v1 samples

`direct_gzip_v1` 冻结协议的 binary 帧样本（S-8，方案 v30 §3.6/§6）。

- **Provenance**: `synthetic-spec-fixture` —— 生产 sender 未实现（freeze-only 单元），
  无真实捕获；本样本冻结 opcode 语义与压缩帧形状。首个真实 wire capture 落地时在
  本 README 记录 raw hash 与 sanitization 边界并替换（对齐 projection-window-v1 纪律）。
- **Canonical rules**: `docs/protocol/bridge-v1.md` §Direct Frame Compression。
- **文件**:
  - `compressed-frame.json.gz` — gzip(JSON envelope) 整帧压缩字节（opcode 0x2 载荷；
    gzip mtime=0、level 6，字节稳定可复现）
  - `compressed-frame.decoded.json` — 解压后的 UTF-8 JSON envelope（与 .gz 内容逐字节
    对应；text 帧 0x1 同形）
- **SHA-256**:
  - `compressed-frame.json.gz`: `09e4ce2baa96cba90d3f4c01609bcbf0209e7194c89e9a5f54fb2c07dc45ee27`
  - `compressed-frame.decoded.json`: `63371826332a8d4ce89d57b4f155809d1c9f7ce0b61f0242a542dd1df2c69925`
- **双仓副本**: Mac `docs/protocol/samples/direct-gzip-v1/` 与 iOS 仓同路径副本必须
  字节一致（spec 测试经各自仓内副本读取）。
- **Decode 验证**: Mac `direct_gzip_spec_test.go` 与 iOS mirror 侧测试共同消费本样本
  （binary → gunzip → 与 decoded.json 深度相等；text 路径输出一致）。
