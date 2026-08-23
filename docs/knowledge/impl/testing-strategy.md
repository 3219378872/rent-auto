# 测试策略

## 金字塔

```
      E2E(dry-run 全链路, 少量)        scheduler 冒烟: 假适配器+真DB+真pricing
    ─────────────────────────
   集成(store/recon/analytics)       testcontainers Postgres; build tag `integration`
  ─────────────────────────────────
 单元(pricing/platform/domain/...)    黄金测试+mock httptest; 默认门控全跑
```

## 门控覆盖要求

- 核心包（pricing、platform/uu、platform/eco、bench、recon、analytics）单测覆盖率 ≥70%
- `make gate` 本地必跑；CI 同套命令 + 集成测试

## 平台客户端契约测试（关键机制）

1. **fixture 交叉验证**：`scripts/gen_uu_fixtures.py` 调用上游 Steamauto 的加密实现，
   对固定输入生成密文向量 → 存 `platform/uu/testdata/` → Go 解码回归。
   保证 Go 加密与 Python 版字节级兼容（RSA PKCS1v15 确定性，AES-ECB 确定性）。
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
- 每用例自建 schema 隔离或事务回滚；禁止依赖执行顺序
- CI 中 TEST_DATABASE_URL 由 service 容器提供

## 手动验收脚本

发布前 runbook 含：dry-run 全任务跑一轮 → price_actions 抽查决策合理性 → 开真实执行灰度 10 商品。
