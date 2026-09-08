# 发布与运维 Runbook

## 构建

```bash
make release   # 后端 linux/amd64 静态二进制 + 前端 dist；不构建 Docker 镜像
docker compose -f deploy/docker-compose.yml build
```

## 部署（docker-compose）

```bash
cd deploy && docker compose up -d     # postgres + backend(+自动迁移) + caddy + backup
docker compose logs -f backend        # 观察启动迁移与健康检查
```

升级：替换镜像 tag → compose up -d（启动时迁移自动前滚；回滚=旧镜像+手工 migrate down 一步）

### TLS 与持久化

- 在 `deploy/.env` 设置 `SITE_ADDRESS=panel.example.com`；Compose 显式把该值注入
  web 容器，Caddy 才会启用自动 HTTPS 和 HTTP 重定向。域名 DNS 必须指向部署主机，
  TCP 80/443 必须可达；`compose config` 正确不等于 ACME 已签证书，发布时另查真实 HTTPS。
- `caddy_data:/data` 保存证书与 ACME 账号，`caddy_config:/config` 保存 Caddy 状态；
  不要执行 `docker compose down -v`，该命令还会删除数据库与备份卷。
- `backend_data:/var/lib/rent-auto` 保存首次口令文件；`pgbackups:/backups` 保存数据库备份。
  这些卷都只防容器替换，不防主机/磁盘损坏，仍需另行配置加密异机备份。
- `.dockerignore` 排除 `.env`、密钥、本地参考件和构建产物，避免被 COPY 进构建镜像；
  真实配置仅通过运行时环境或受限文件注入。

### 首次管理员口令与改密

- 未配置 `ADMIN_PASSWORD_HASH` 且数据库尚无管理员密码时，后端生成随机口令，
  以独占创建、`0600` 权限写入 `ADMIN_BOOTSTRAP_FILE`。Compose 路径固定为
  `/var/lib/rent-auto/admin-password`；目录 `0700`、属主为非 root 运行用户 `app`（UID 10001）。
  日志仅显示文件路径，不再输出口令。如果文件写入成功、数据库初始化中断，下次启动仅在
  数据库仍无密码 hash 时从已有文件恢复口令；文件必须为非符号链接的普通文件、权限恰为
  `0600`，内容必须为 36 位十六进制口令。不符合条件或无法安全写入时初始化失败，不覆盖旧文件。
  数据库已有密码 hash 时直接使用数据库值，不读取初始文件。
- 首次使用者在受信任、未录屏/未采集终端输出的管理员终端读取：

  ```bash
  docker compose exec backend cat /var/lib/rent-auto/admin-password
  ```

  使用该口令登录后，通过侧栏「修改密码」设置新密码（UTF-8 为 12–72 字节）。
  `PUT /api/v1/auth/password` 要求当前密码，成功后密码与会话纪元在同一事务更新，
  全部旧 JWT（包括当前会话）立即失效，面板返回登录页。初始文件不会随改密更新，
  其中旧口令已不可登录；验证新密码可以登录并记录交付完成后，应由管理员删除这一个旧文件。
  若有意重建空数据库，必须先归档或删除旧初始文件，否则初始化会恢复其中的旧口令。
- 直接运行二进制时可用 `ADMIN_BOOTSTRAP_FILE` 指定受限文件；绑定主机目录时必须预先
  创建目录并赋予 UID 10001 写权限，不能用 `0777` 绕过权限错误。
- 配置非空 `ADMIN_PASSWORD_HASH` 时，认证由部署环境中的 bcrypt hash 托管，不生成初始文件，
  面板改密返回 `409 password_managed_externally`。`.env` 中的 bcrypt 值需用单引号保护 `$`。
  外部轮换应同时轮换 `JWT_SECRET` 并重建 backend，使既有 JWT 失效；仅改 hash 不会改变会话纪元。
  删除外部覆盖后会重新使用数据库中已有的密码，不能假设环境中的新 hash 已写回数据库。

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
- **代码更新（标准流，2026-08-28 定）**：本机改码 → `git push origin main`
  （pre-push 钩子自动跑 `make gate`）→ 远端 `cd ~/rent-auto && git pull --ff-only
  origin main` → `sudo docker compose up -d --build`（启动时迁移自动前滚）。
  注意远端 `deploy/.env`、`backend/.env` 均已 gitignore，pull 不冲突；
  origin URL 内嵌 GitHub PAT（随 .git 自本机迁入），勿在远端 log/echo 该 URL
- **⚠️ APP_MASTER_KEY 双 key 纪律**：本地 dev 后端（`make server`）用
  `backend/.env` 的 key 加密渠道凭证；远端 `deploy/.env` 必须与之**同 key**，
  否则解密失败、渠道页显示为空（2026-08-28 迁移事故，见 evidence 同日文档）。
  改任一 key 前先确认对方存量密文口径
- **数据搬迁**：`pg_dump -Fc` → scp → `pg_restore --clean --if-exists`
  （schema_migrations 随行，backend 启动不重复迁移）
- **跨机双开红线**：本地 `make server` 与远端实例**不可同时运行**——advisory
  lock 按库加锁，拦不住两实例各自操作同一批平台账号

## 备份

- Compose 的 `backup` 服务基于与数据库同主版本的 PostgreSQL 16，以非 root `postgres`
  用户运行 `deploy/backup.sh`。启动立即执行一次 `pg_dump --format=custom --no-owner --no-acl`，
  成功后间隔 86400 秒再执行；失败不清理既有备份，默认 300 秒重试。它不是 cron，也不固定墙钟时刻。
- 文件仅在 `/backups/rent-auto`（目录 `0700`）创建：先写隐藏 `.partial` 文件（`0600`），
  `pg_dump` 成功且 `pg_restore --list` 校验通过后才在同目录原子改名为
  `rent-auto-YYYYMMDDTHHMMSSZ-XXXXXXXX.dump`。失败/正常终止清理本次临时文件，不发布半成品。
- 仅在成功发布后删除该专用目录中、文件名匹配上述格式且修改时间已满 14 天的普通 dump 文件。
  不递归、不追随符号链接、不删除其他名称或其他目录；不要把自有手工文件命名成这一格式。
- `BACKUP_INTERVAL_SECONDS` 可设为 60–86400，`BACKUP_RETRY_SECONDS` 为 1–3600。
  `BACKUP_ONCE=true` 用于一次性操作，成功退出 0，失败退出非零；数据库凭证由 `PG*` 环境传入，
  不写入备份名或命令参数。连续 36 小时没有新的成功 dump 时容器健康检查失败；发布后应监控状态和日志。

  ```bash
  docker compose ps backup
  docker compose logs --tail 50 backup
  docker compose run --rm -e BACKUP_ONCE=true backup
  docker compose exec backup ls -l /backups/rent-auto
  ```

### 恢复演练（每月）

只恢复到新建的专用测试库；以下 `rentauto_restore_test` 若已存在，先确认所有者与用途，
不要直接覆盖或清空。选择一个已完成的 `.dump` 文件，不能使用 `.partial`。

```bash
docker compose exec postgres createdb -U rentauto rentauto_restore_test
docker compose run --rm --no-deps --entrypoint pg_restore backup \
  --exit-on-error --no-owner --no-acl --dbname=rentauto_restore_test \
  /backups/rent-auto/<已完成备份文件>.dump
docker compose exec postgres psql -U rentauto -d rentauto_restore_test \
  -c 'SELECT count(*) FROM templates; SELECT count(*) FROM lease_orders;'
```

核对迁移版本、关键表计数和抽样订单金额，记录恢复耗时与备份时间。真实备份包含加密凭证，
演练库不能接入自动化实例；验证后由操作者明确删除这一个测试库。数据库 dump 不包含环境变量中的
`APP_MASTER_KEY`/`JWT_SECRET`，密钥必须通过独立受限渠道备份；缺少原 master key 时无法解密渠道凭证。

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
- [ ] 实际 HTTPS 证书与 HTTP 重定向通过（不是只检查 Compose 配置）
- [ ] 首次口令文件权限/非 root 属主正确，初次登录后完成改密
- [ ] backup 成功产出 `0600` dump，隔离库恢复演练通过；异机副本另行确认
- [ ] 版本号打 git tag（vX.Y.Z），CHANGELOG 记录于 evidence/release/
