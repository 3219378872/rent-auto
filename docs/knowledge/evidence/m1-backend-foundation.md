# M1 后端骨架 — 验证证据

日期：2026-08-23 ｜ 分支：feat/m1-backend-foundation

## 交付范围

- `backend/`：Go 1.26 模块；config(环境变量) / logging(slog JSON) / domain / secrets(AES-GCM) /
  auth(HS256 JWT + bcrypt) / store(pgx v5 + 自研内嵌迁移器 + advisory lock 单实例防护) /
  api(net/http 路由、JWT 中间件、recover/日志中间件、login/me/health)
- `cmd/migrate` CLI：up/down/status
- 迁移：0001_init(app_settings, audit_log)，双向可逆，嵌入 FS 启动自动升级
- OpenAPI 契约 v0.2.0（health/auth 组）
- ADR 补充说明见 adr-0001 文件（迁移器改为自研轻量实现，理由：避免 golang-migrate 依赖树）

## 执行证据

```
gofmt -l            → 空
golangci-lint run   → 0 issues（v2 配置格式已迁移）
go vet              → 通过
go build ./...      → 通过
go test -race -tags=integration ./... → 全绿（含迁移 up→down-all→up 循环、settings/audit CRUD）
coverage total      → 72.4%（api 78.5% auth 90.9% config 76.2% secrets 79.2% store ≥64%各函数）
```

## 已知偏差与理由

- 门控 Makefile 的 test 目标现会自动探测本地 15432 端口决定是否带集成测试，
  保证覆盖率统计口径包含 DB 路径（AGENTS.md 门控定义第1条的执行细则）
- golangci-lint 升级至 v2.6.0 新配置 schema（`version: "2"`），gosimple 并入 staticcheck
- jsonb 取回字符串含规范化空格——store 测试以解析后语义比较而非字节比较

## Open Questions → spec

- 无新增
