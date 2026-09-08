# 本地开发环境

## 依赖

- Go ≥1.26、Node ≥22 + pnpm、Docker（Postgres）、golangci-lint
- 可选：Python3（仅 scripts/ 里生成加密 fixture 用）

## 首次启动

```bash
cp deploy/.env.example deploy/.env    # 先填写JWT_SECRET/APP_MASTER_KEY/数据库密码，Compose会解析全配置
make dev-up                    # 起 docker-compose: postgres:16（宿主机端口 PG_HOST_PORT，默认 15432）
cp backend/.env.example backend/.env   # 填 JWT_SECRET/APP_MASTER_KEY(32B hex)
make server                    # 自动加载 backend/.env（仅填充未导出的变量，外部环境优先）
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
| ADMIN_PASSWORD_HASH | 可选 | bcrypt；有值时由部署管理，面板改密返回409 |
| ADMIN_BOOTSTRAP_FILE | 可选 | 未指定hash时，初始随机口令独占写入0600文件；默认用户配置目录 rent-auto/admin-password |
| DRY_RUN_DEFAULT | — | 默认 true |
| TRUST_PROXY_CIDRS | — | 可设置 X-Real-IP 的对端 CIDR（逗号分隔）；默认私网+回环——backend 端口直接公网暴露时必须收紧为仅回环并由代理覆写头 |
| LOG_LEVEL | — | debug/info/warn |

`make server` 自动加载 `backend/.env`（.gitignore 已忽略）：仅填充 shell 未导出的变量，
外部环境优先于 .env；此加载器使用不加引号的 KEY=value，不执行 shell 内容。注意：JWT_SECRET/APP_MASTER_KEY
一经使用须保持稳定——更换 JWT_SECRET 使面板会话失效，更换 APP_MASTER_KEY 使已存渠道凭证无法解密。

## 集成测试

```bash
export TEST_DATABASE_URL=postgres://rentauto:rentauto@localhost:15432/rentauto_test?sslmode=disable
make test-integration          # 库必须预先存在且可丢弃；不会回退 DATABASE_URL
```

首次启动只记录口令文件路径，不记录口令；用本机文件权限读取初始口令，登录后经侧栏改密，
成功后全部会话退出。确认新口令可登录后删除该初始文件。已有文件拒绝覆盖；DB hash 缺失时
允许从符合权限和格式的既有文件恢复中断的初始化。刻意空库重建前应先安全归档或移除旧文件，
否则会恢复旧的初始口令；DB 中已有 hash 时不读取此文件。

## 常见问题
- ECO 时间戳失效(5003)：校准系统时钟
- UU 行情 84104：触发了风控限频，调低 scheduler 并发
