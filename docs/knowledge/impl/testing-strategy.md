# 测试策略

## 金字塔

```
      E2E(dry-run 全链路, 少量)        scheduler 冒烟: 假适配器+真DB+真pricing
    ─────────────────────────
   集成(store/recon/analytics)       显式隔离 Postgres; build tag `integration`
  ─────────────────────────────────
 单元(pricing/platform/domain/...)    黄金测试+mock httptest; 默认门控全跑
```

## 门控覆盖要求

- 纯逻辑包（pricing、platform 及其各渠道、recon、analytics、auth、secrets、config）逐包覆盖率 ≥70%
- store/api/bench/scheduler 编排层用真实测试库集成验证，不设置行覆盖率配额
- `make gate` 本地必跑；CI 同套命令 + 集成测试

## 平台客户端契约测试（关键机制）

1. **fixture 交叉验证**：AES-128-ECB/PKCS7 为确定性算法——以 `openssl enc -aes-128-ecb`
   生成的密文向量作为第三方基准（testdata/p*.b64），Go 实现 byte-level 对齐；
   RSA-PKCS1v15 加密含随机填充，采用"Go 加密→openssl 私钥解密"与
   "openssl 加密→Go 解密"双向互操作验证替代固定向量。
   （上游 Python 参考件的加密实现与本方案同源：PKCS7+ECB+PKCS1v15 均为标准算法规格）
2. **mock 重放**：httptest server 按逆向笔记构造响应体；每个端点至少：成功/业务错误码/风控码 三例。
3. **签名往返**：ECO 用仓库内专用测试 RSA 密钥对自签自验 + 固定向量比对。

## 定价引擎黄金测试

- 输入行情数组→输出 Decision 断言（含与 Steamauto 行为一致的移植用例）
- 护栏矩阵表驱动：每条护栏至少一命中一放行

## Reconciler 差异矩阵

desired×actual 组合表驱动：(active,none)=上架 / (none,active)=下架 /
(active@uu, active@eco) 按 route 判定保留或收敛 …

## 集成测试约定

```go
//go:build integration
```
- 只接受 TEST_DATABASE_URL，不回退 DATABASE_URL；数据库名必须以 test_ 开头或 _test 结尾。
- 本地与 CI 共享一套数据库的测试包必须 -p 1；fixture 显式清理其表，迁移测试会 DROP 全表。
- CI 中 TEST_DATABASE_URL 由 service 容器提供；本地先创建可丢弃库再显式指定连接。
- R01-R36 回归覆盖包括失锁取消、epoch 并发、完整快照/分页、持续孤儿计时、最终价格护栏、
  稀疏策略并发验证、财务镜像去重与订单冲销。Mock/race/浏览器结果不替代真实平台验收。

## 手动验收脚本

发布前 runbook 含：dry-run 全任务跑一轮 → price_actions 抽查决策合理性 → 开真实执行灰度 10 商品。
