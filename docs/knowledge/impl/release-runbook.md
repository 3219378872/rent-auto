# 发布与运维 Runbook

## 构建

```bash
make release   # 后端 linux/amd64 静态二进制 + 前端 dist + docker image
```

## 部署（docker-compose）

```bash
cd deploy && docker compose up -d     # postgres + backend(+自动迁移) + caddy(TLS)
docker compose logs -f backend        # 观察启动迁移与健康检查
```

升级：替换镜像 tag → compose up -d（启动时迁移自动前滚；回滚=旧镜像+手工 migrate down 一步）

### 数据库端口纪律（2026-08-24 起）

- compose 中 Postgres 宿主机端口**固定绑定 `127.0.0.1:` 回环**（security-spec：
  数据库不暴露非本机网络）；`PG_HOST_PORT` 只改宿主机端口号，不改绑定点
- `POSTGRES_PASSWORD` 的 `rentauto` 弱默认仅因回环绑定而可接受；
  任何跨机访问需求一律走 backend 容器内网（`postgres:5432`），禁止改绑 `0.0.0.0`
- ⚠️ 未设置 `SITE_ADDRESS` 时 Caddy 退化为 :80 明文——仅限内网调试，公网部署必须设置

### 主机部署（ubuntu，2026-08-28 起）

- **入口**：`http://100.102.138.9:8081`（Tailscale IP；ufw 仅放行 tailscale0，
  纯 HTTP 明文，公网/局域网不可达——面板持平台凭证，勿放宽 ufw）
- **位置**：`lee@ubuntu:~/rent-auto/deploy`；`lee` 不在 docker 组，一律
  `sudo docker compose …`（免密 sudo）
- **代码更新**：开发机 `tar czf - --exclude=node_modules --exclude=dist --exclude=bin --exclude=Steamauto . | ssh lee@ubuntu 'tar xzf - -C ~/rent-auto'`
  → 远端 `sudo docker compose up -d --build`（启动时迁移自动前滚）
- **⚠️ APP_MASTER_KEY 双 key 纪律**：本地 dev 后端（`make server`）用
  `backend/.env` 的 key 加密渠道凭证；远端 `deploy/.env` 必须与之**同 key**，
  否则解密失败、渠道页显示为空（2026-08-28 迁移事故，见 evidence 同日文档）。
  改任一 key 前先确认对方存量密文口径
- **数据搬迁**：`pg_dump -Fc` → scp → `pg_restore --clean --if-exists`
  （schema_migrations 随行，backend 启动不重复迁移）
- **跨机双开红线**：本地 `make server` 与远端实例**不可同时运行**——advisory
  lock 按库加锁，拦不住两实例各自操作同一批平台账号

## 备份

- Postgres 每日 dump（compose 内置 cron 服务）保留14天；恢复演练每月一次
- 恢复：`gunzip < dump.sql.gz | docker exec -i pg psql ...`

## 故障处理

| 症状 | 动作 |
|---|---|
| UU 登录失效告警 | 面板渠道页重新短信登录；期间路由 fallback 至 ECO 自动生效 |
| ECO 5003/5004 | 校时 (`chrony tracking`)；核对私钥指纹是否被更换 |
| 大量 skip 决策 | 看 price_actions.decision.reasons：多为 V 缺失或护栏命中 |
| 双开防护触发 | advisory lock 未释放：确认无第二实例后重启 |

## 发布检查单

- [ ] make gate 全绿
- [ ] dry-run 任务全链路过一遍（price_actions 抽查 ≥5 条）
- [ ] 迁移在 staging 库 up/down/up 通过
- [ ] 版本号打 git tag（vX.Y.Z），CHANGELOG 记录于 evidence/release/
