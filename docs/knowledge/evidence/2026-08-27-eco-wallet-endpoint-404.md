# 2026-08-27 ECO 钱包余额端点 404 复盘与修复

## 范围

真机运行报 `eco: /Api/Merchant/GetMerchantMoney http status 404`。
定位为端点路径转录错误（设计层文档与实现同源同错），非平台故障。

## 根因

- 2026-08 抓取官方文档时，`platform-eco-api-notes.md` 端点表把「余额查询」记为
  `/Api/Merchant/GetMerchantMoney`；该路径在 openapi.ecosteam.cn 上**不存在**，
  HTTP 404 → 客户端按严格策略 fail-closed（行为正确，暴露了路径错误）。
- 2026-08-27 重新抓取 docx.ecosteam.cn 权威 OpenAPI YAML（apifox 导出，api-220956645）：
  - 余额查询实际路径 **`/Api/Merchant/GetTotalMoney`**（获取用户钱包金额）
  - 资金流水实际路径 **`/Api/Merchant/GetFundFlow`**（本项目尚未实现，仅文档表记录错）
  - 官方索引首页只给功能名不带路径——路径必须逐页进 OpenAPI YAML 抄录，
    此教训已写入 api-notes「已知坑 #7」。

## 修复

- `backend/internal/platform/eco/endpoints.go`：`GetWalletBalance` 改用
  `/Api/Merchant/GetTotalMoney`。
- mock 佐证（硬规则：外部可见行为改动必须有 mock 测试）：
  - 新增 `TestWalletBalanceHitsDocumentedRoute`（client_test.go）：断言请求路径
    精确等于文档路径 + `ResultData.Money` 解码，防路径漂移回归。
  - `adapter_test.go` mock 路由同步改名。
- 响应 schema 官方为 `MerchantMoneyModel`（Money/LockMoney/PurchaseMoney/
  WaitSettlementMoney 等）；当前仅消费 `Money`，其余字段留待需求出现再接。

## 验证

- `make gate PG_HOST_PORT=25432` 全绿（含集成测试与迁移升降级检查）：
  lint 0 issues / vet / build / `go test -race` 全过；eco 包覆盖 78%（门控 ≥70%）。
- 前端 tsc/eslint/vitest(35)/build 四绿。
- 备注：本机 Postgres 容器实际映射 127.0.0.1:25432（compose 项目以
  PG_HOST_PORT=25432 启动），默认探测 15432 落空会静默降级 unit-only，
  analytics 覆盖率掉到 10% 触发 cover-gate 误报——复跑带 `PG_HOST_PORT=25432`
  后恢复 81%。环境事实记录，不改仓库默认值。

## 文档同步

- `design/platform-eco-api-notes.md`：端点表两行路径修正 + 已知坑 #7 新增。
- 待办 #E1–#E3 不受影响；资金流水路径修正仅涉文档（无实现）。
