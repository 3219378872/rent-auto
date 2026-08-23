# 本地开发环境

## 依赖

- Go ≥1.26、Node ≥22 + pnpm、Docker（Postgres）、golangci-lint
- 可选：Python3（仅 scripts/ 里生成加密 fixture 用）

## 首次启动

```bash
make dev-up                    # 起 docker-compose: postgres:16（宿主机端口 PG_HOST_PORT，默认 15432）
cp backend/.env.example backend/.env   # 填 JWT_SECRET/APP_MASTER_KEY(32B hex)
DATABASE_URL=postgres://rentauto:rentauto@localhost:$PG_HOST_PORT/rentauto?sslmode=disable make server
make web                       # 前端 :5173（代理 /api → 8080）
```

宿主机 15432 被占用时：在 `deploy/.env` 设 `PG_HOST_PORT=25432` 后重新 `make dev-up`，
并让门控/集成测试使用同一端口：`make gate PG_HOST_PORT=25432`
（或 `export TEST_DATABASE_URL=postgres://rentauto:rentauto@localhost:25432/rentauto?sslmode=disable`）。
⚠️ pre-push 钩子执行裸 `make gate`（默认 15432）：端口非默认的机器请
`export PG_HOST_PORT=25432` 后再 push，否则探测失败会静默跳过集成测试，
analytics 等依赖 DB fixture 的包将卡在覆盖率门控上。

## 环境变量（backend）

| 变量 | 必填 | 说明 |
|---|---|---|
| DATABASE_URL | ✓ | postgres://... |
| JWT_SECRET | ✓ | ≥32 字节随机 |
| APP_MASTER_KEY | ✓* | 32 字节 hex；凭证加密主密钥（生产必填） |
| ADMIN_PASSWORD_HASH | 首启 | bcrypt；未提供则日志打印一次性初始化链接 |
| DRY_RUN_DEFAULT | — | 默认 true |
| LOG_LEVEL | — | debug/info/warn |

## 集成测试

```bash
make test-integration          # 需要 TEST_DATABASE_URL（或自动 testcontainers）
```

## 常见问题
- ECO 时间戳失效(5003)：校准系统时钟
- UU 行情 84104：触发了风控限频，调低 scheduler 并发
