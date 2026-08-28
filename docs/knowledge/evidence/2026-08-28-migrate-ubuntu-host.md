# 2026-08-28 项目迁移至 ubuntu 主机（面板 8081 / Tailscale）

## 范围

将整套 rent-auto（Postgres 数据 + Go 后端 + 前端/Caddy）从开发机迁移至
`lee@ubuntu`（Ubuntu 26.04 LTS，Tailscale `100.102.138.9`），以 docker-compose
长期运行。面板端口 8081，ufw 仅放行 tailscale0 接口。含渠道凭证解密失败
（双 APP_MASTER_KEY 不一致）的根因定位与修复记录。

## 执行过程与结果

### 1. 传输与环境确认

- 本地无 rsync，改用 `tar czf - --exclude=node_modules/dist/bin/coverage/Steamauto . | ssh tar xzf`，
  仓库 + `deploy/.env` + `backend/.env` 共 9.7M 落至 `ubuntu:~/rent-auto`
- 远端预检：Docker 29.1.3 + Compose v2.40.3 已装；`lee` 不在 docker 组 →
  全程 `sudo docker`；8081 空闲（9090 被 mihomo 外部控制端口占用故弃用）；
  15432 空闲（Postgres 回环绑定沿用）

### 2. 远端 deploy/.env

| 项 | 处置 | 理由 |
|---|---|---|
| `WEB_PORT` | 8081 | 用户指定 |
| `POSTGRES_PASSWORD` / `JWT_SECRET` | 换 `openssl rand -hex` 强随机 | runbook 纪律：非回环部署不得弱默认 |
| `APP_MASTER_KEY` | 先沿用 deploy 旧值 → **后改为本地 backend/.env 的值**（见 §4） | 解密存量凭证 |
| 密钥注入方式 | stdin 管道 + 远端 sed，不经 shell 历史/ps | 凭证卫生 |

### 3. 数据迁移

- 本地 `pg_dump -Fc rentauto`（98K）→ scp → 远端先 `up -d postgres` →
  `pg_restore --clean --if-exists`
- 远端行数核对：listings=5 / lease_orders=5 / templates=56 / strategies=1 /
  schema_migrations=0008 —— 与迁移前本地一致，backend 启动无重复迁移
- `app_settings` 密文随库带入：eco_creds(2440B) / steam_creds(232B) /
  steam_tokens(1516B) / uu_token(460B)

### 4. 事故：渠道页账号为空（双 APP_MASTER_KEY 不一致）

- **现象**：迁移完成、面板可开，但 `/#/channels` 无任何渠道账号
- **根因**：本地长期以 `make server` 跑 dev 后端，加载 `backend/.env` 的
  APP_MASTER_KEY（凭证入库时用它加密）；docker-compose 部署读 `deploy/.env`
  ——两文件 key 不同。远端沿用 deploy 旧 key，解密失败表现为"无账号"。
  本地从未跑过 compose backend，deploy key 名下无任何存量密文，切换安全。
- **修复**：远端 deploy/.env 的 APP_MASTER_KEY 换为本地 backend/.env 的值，
  `docker compose up -d backend` 重建
- **修复后正向验证**（backend 日志，12 分钟窗口零 WARN）：
  - `steam offers accepted=0 skipped_costly=3`——steam_tokens 解密成功且真实
    调用 Steam（读到 3 条 costly 报价）
  - `uu delivery sent=0 gifts_skipped=0 err=null`——uu_token 解密成功
  - eco_delivery / orders_sync 每 5/10 分钟周期任务静默成功（成功路径不打日志，
    仅错误记 WARN，窗口内 WARN=0）
  - 日志中 `decrypt|master` 关键词命中 0 次

### 5. 部署验证

- `docker compose ps`：postgres healthy（127.0.0.1:15432 回环纪律保持）/
  backend v0.7.0 listening :8080 / web 0.0.0.0:8081→80
- HTTP 探针：`/` 200 (text/html)、SPA 回退 `/listings` 200、
  `/api/v1/health` 200、`/api/v1/auth/me` 401（鉴权链路正常）
- Tailscale 跨机：本机 `curl http://100.102.138.9:8081/{,/api/v1/health}` 双 200
- ufw：`allow in on tailscale0 to any port 8081 proto tcp`，公网不可达

### 6. 双实例防护（切换）

- 本地 `make server` 进程（go run → :8080）已 kill——advisory lock 按库加锁
  拦不住跨机双实例，两套实例会同时操作同一批平台账号
- 本地 Postgres 容器保留作开发库；本地 `deploy/.env` 与远端自此分叉（远端为生产口径）

## 已知偏差与理由

- **HTTP 明文**：未设 SITE_ADDRESS，Caddy 退化 :80（→8081）。缓解：ufw 仅
  tailscale0 放行，公网与局域网均不可达；若需公网必须设域名走 Caddy 自动 HTTPS
- **JWT_SECRET 已轮换**：所有旧面板会话失效，需重新登录（管理员账号随库迁移不变）
- **代码更新流程**：远端走 tar-over-ssh 全量推送（本次实操）；远端 .git 亦随包
  迁入可作 git pull 备选
- **运行手册**：已补 `impl/release-runbook.md`「主机部署（ubuntu）」一节，
  含双 APP_MASTER_KEY 纪律（backend/.env 与远端 deploy/.env 必须同 key）

## 覆盖率

纯运维迁移，无代码变更，N/A（门控由 push 钩子对 main 全量执行）。
