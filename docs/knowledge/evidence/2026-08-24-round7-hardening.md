# 2026-08-24 工程收口轮（round7）

## 范围

round6 移交的三项工程欠账：PublishLease 哨兵化 / gosec 引入 / Vite7+Vitest3 升级。

## 交付清单

| 项 | 内容 | 验证 |
|---|---|---|
| PublishLease 部分失败哨兵 | `platform.PartialIfAnyFailed`：任一逐项失败以 ErrPartialFailure 暴露、结果数组保持权威；uu/eco 接入；recon 执行器按逐项判定（成功项写回计数，逐项拒绝不误触风控退避）；审计 detail 取 item remark | 先行 mock 反例 uu/eco 各一 + recon 哨兵路径单测；存量断言按新契约更新 |
| gosec 引入门控 | 首跑 31 项逐条分诊至零告警：规则级豁免 6 类（G404/G115/G117/G124/G401/G505，理由随行注释）、定向 nolint 2 处（G704 SSRF=上游固定域名回放 / G101=存储键名非凭证）、测试路径豁免 | `golangci-lint run` 0 issues |
| Vite7/Vitest3 升级 | vite ^5.4→^7.3.6 / plugin-react ^4.3→^5（6.x 与 vite7.3 存在 exports 不兼容，回落稳定线）/ vitest ^1.6→^3.2.7 / jsdom ^24→^30；pnpm.onlyBuiltDependencies 批准 esbuild | tsc/eslint/vitest 19 用例/vite build 四绿 |
| ADR-0003 收口 | 后果节更新：哨兵不对称（PartialError/PublishLease）自此清零 | 文档评审 |

## 执行的命令与结果

```
make gate   # GATE PASSED
golangci-lint run ./...   # 0 issues（含 gosec）
cd frontend && pnpm exec tsc --noEmit && pnpm exec eslint . --max-warnings=0 \
  && pnpm exec vitest run && pnpm exec vite build   # 全绿
```

## 已知偏差与理由

- @vitejs/plugin-react 固定 ^5 而非最新 6.x：6.1.0 引用 `vite/internal` 子路径在
  vite 7.3.6 下 ERR_PACKAGE_PATH_NOT_EXPORTED，待其兼容矩阵明确后再升
- esbuild 构建脚本需显式批准（pnpm v10 安全默认）：仅 esbuild 一项入白名单
- gosec 的 G115 为整类豁免：10 处全部位于 Steam/UU 线协议编解码
  （SteamID uint64↔int64、protobuf 长度守卫、PKCS7 pad），边界均经 round3/5 审查验证；
  若未来新增协议外转换应改用定点 nolint

## 移交后续

1. 特性入口：模板级策略 UI（后端 template scope CRUD API 缺位）、ECO 回调/WebSocket、
   出售域适配器、因子参数面板化
2. foldOrderEvents UU 渠道依赖真机字段确认（api-notes 待办#14）
3. plugin-react 6.x 兼容性跟进
