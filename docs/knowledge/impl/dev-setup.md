# 本地开发环境

## 依赖

- Go ≥1.26、Node ≥22 + pnpm、Docker（Postgres）、golangci-lint
- 可选：Python3（仅 scripts/ 里生成加密 fixture 用）

## 首次启动

```bash
make dev-up                    # 起 docker-compose: postgres:16 (端口15432)
cp backend/.env.example backend/.env   # 填 JWT_SECRET/APP_MASTER_KEY(32B hex)
make server                    # 后端 :8080（启动自动跑迁移）
make web                       # 前端 :5173（代理 /api → 8080）
```

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
