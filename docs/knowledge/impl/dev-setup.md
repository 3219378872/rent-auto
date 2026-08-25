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
（或 `export TEST_DATABASE_URL=postgres://rentauto:rentauto@localhost:25432/rentauto_test?sslmode=disable`——
门控一律连 **rentauto_test** 测试库，迁移检查会 DROP 全表，绝不指向开发库）。
⚠️ pre-push 钩子执行裸 `make gate`（默认 15432）：端口非默认的机器请
`export PG_HOST_PORT=25432` 后再 push，否则探测失败会静默跳过集成测试，
analytics 等依赖 DB fixture 的包将卡在覆盖率门控上。

钩子接线（新 clone 必做一次，否则推送前门控形同虚设）：

```
make hooks    # = git config core.hooksPath .githooks
```

lint 工具链与 CI 精确一致（round7 教训：版本差会让规则集/config verify 静默漂移）：

```
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.1
```

## 环境变量（backend）

| 变量 | 必填 | 说明 |
|---|---|---|
| DATABASE_URL | ✓ | postgres://... |
| JWT_SECRET | ✓ | ≥32 字节随机 |
| APP_MASTER_KEY | ✓* | 32 字节 hex；凭证加密主密钥（生产必填） |
| ADMIN_PASSWORD_HASH | 首启 | bcrypt；未提供则日志打印一次性初始化链接 |
| DRY_RUN_DEFAULT | — | 默认 true |
| TRUST_PROXY_CIDRS | — | 可设置 X-Real-IP 的对端 CIDR（逗号分隔）；默认私网+回环——backend 端口直接公网暴露时必须收紧为仅回环并由代理覆写头 |
| TRUST_PROXY_CIDRS | — | 允许设置 X-Real-IP 的对端 CIDR 逗号分隔；缺省=回环+RFC1918+ULA（backend 端口不发布的部署下安全；公网直连必须收紧） |
| LOG_LEVEL | — | debug/info/warn |

## 集成测试

```bash
make test-integration          # 需要 TEST_DATABASE_URL（或自动 testcontainers）
```

## 常见问题
- ECO 时间戳失效(5003)：校准系统时钟
- UU 行情 84104：触发了风控限频，调低 scheduler 并发
