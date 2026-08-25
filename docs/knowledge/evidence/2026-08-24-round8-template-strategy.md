# 2026-08-24 模板级策略轮（round8）

## 范围

最后一个登记的面板功能缺口：US-STRAT-02 模板级覆盖策略（后端 CRUD API +
前端编辑器），含一处存量语义缺陷修复。

## 交付清单

| 项 | 内容 | 验证 |
|---|---|---|
| template scope CRUD | `POST /strategies/template`（每模板一行 upsert；新建行默认 dry-run=AC-T1；更新缺省保留 real 值）+ `DELETE /strategies/template/{id}`（立即回落全局）；参数经 pricing.ParseParams 深解析校验（类型错误 400 而非运行时静默 skip）；审计 strategy.template_upsert/delete | TestTemplateStrategyLifecycle / Validation |
| fix: 生效策略合并缺陷 | GetEffectiveStrategy 原实现只合并模板 params——**route 与 real_execution_enabled 被整体忽略**，与 spec/帮助文案承诺的深覆盖相悖；补齐 COALESCE 合并。该修复同时影响 reprice 门禁（模板行可独立控制 dry-run）与 recon 路由读取口径 | 生命周期断言 route/real/params 三层合并 |
| 面板编辑器 | 抽取 ParamGroupsEditor 共享组件（全局表单与模板编辑器同源控件）；Strategies 页新增模板策略列表+新建/编辑/删除；黑名单模板不可选 | vitest 20 用例（新增区块渲染/黑名单过滤/保存载荷） |
| openapi v0.5.0 | 新增两条路径 → **23 路由全覆盖** | yaml 解析通过 |

## 执行的命令与结果

```
make gate   # GATE PASSED
go test -tags=integration -race -count=1 -p 1 ./...   # 全绿
```

## 过程记录

- upsert 冲突目标首版写 `(hash_name) WHERE scope='template'` 无法匹配部分索引
  （索引列为 (scope,hash_name)），Postgres 报 no unique constraint matching；
- 排查中发现并修复上述 GetEffectiveStrategy 存量缺陷（生命周期测试先行暴露）
- upsert keep-real 首版用 EXCLUDED 引用——EXCLUDED 取 INSERT 计算值
  （COALESCE($6,false)），keep 语义失效；改回直接引用 $6

## 移交后续

1. ECO 回调/WebSocket、出售域适配器、因子参数面板化（特性入口）
2. foldOrderEvents UU 渠道依赖真机字段确认（api-notes 待办#14）
3. plugin-react 6.x 兼容性跟进；golangci-lint 本地/CI 版本差收敛（round7 教训）
