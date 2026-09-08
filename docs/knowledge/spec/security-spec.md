# 安全规格

## 凭证管理

| 凭证 | 存储位置 | 加密 | 展示规则 |
|---|---|---|---|
| UU token | app_settings.value_enc | AES-256-GCM(APP_MASTER_KEY) | 仅尾8位 |
| Steam 账号+Guard密钥 / tokens | app_settings.value_enc | AES-256-GCM | 不可见 |

> 存量迁移：0005 之前 UU token 曾明文存于 value_plain；Registry.Refresh 启动时
> 自动惰性迁移为 value_enc 并清除明文（UpsertSettingEnc 重写时强制置空 value_plain）。
| ECO partnerId | app_settings.value_plain | —（非密） | 明文 |
| ECO RSA 私钥(PKCS8) | app_settings.value_enc | AES-256-GCM | 仅显示指纹(SHA256前12位) |
| 面板管理员密码 | app_settings（bcrypt cost≥10） | bcrypt | 不可见 |
| JWT 密钥 | env JWT_SECRET（≥32B） | — | 启动时缺失则拒绝启动 |
| APP_MASTER_KEY | env（32B hex） | — | 同上 |

- 禁止：凭证写入日志、审计 detail、错误信息、API 响应
- 凭证变更必须产生 audit_log（动作+操作者+尾号指纹，不含明文）
- 首次管理员口令不得写入普通日志：优先设置 `ADMIN_PASSWORD_HASH`；未设置时只写
  `ADMIN_BOOTSTRAP_FILE` 指定的独占创建、权限 0600 的文件。已存在文件拒绝覆盖。
  若文件交付后数据库写入中断，只有在数据库中尚无 hash 时才允许从格式正确、0600、
  非符号链接的既有文件恢复；有 hash 时永远以数据库为准，不读取旧口令文件。
  管理员可经受保护的改密接口提交当前口令与新口令，成功后立即吊销全部会话。

## 会话与吊销（ADR-0006）

- 面板 JWT 为 HS256 + exp(≤24h，可经 `JWT_TTL` 下调如 `12h`；超 24h 钳制 24h)，claims 携带会话纪元 `ver`；
  header.alg 显式要求 HS256（拒 none/混淆），sub 非空、iat 漂移 ≤60s；store 不可用时 fail-closed 401
  （2026-09-08 补齐生产调用链；9 月 3 日历史证据未覆盖此分支）
- 仅“纪元键不存在”视为初始 0；读取/解析错误必须拒绝。登录不能在未知纪元签发 token，
  登出/改密仅在数据库事务提交后返回成功，纪元递增必须数据库原子化。
- 登录先读取纪元，再取得密码快照。并发改密期间完成的旧密码验证不能签出新纪元会话。
- UU 短信发送按 IP 限流（10 次/10min，`handleUUSms`），verify 入口同限；失败记 `channel.uu.sms_failed` 审计；429 记 `*.rate_limited` 审计
- 写通道与任务触发按 IP 限流（30 次/10min，`PUT /channels/*`、`POST /jobs/*/run`）
- 吊销机制：`POST /api/v1/auth/logout` 使 `jwt_session_epoch` +1，
  全部旧 token 立即 401（单管理员语义=登出所有会话）
- 登录限流：per-(IP,username) 固定窗口锁定；条目超阈值自动清扫
  （2026-08-24 第三轮落地）

## 传输与部署

- 生产部署必须 TLS：deploy/Caddyfile 设置 `SITE_ADDRESS=<域名>` 后由 Caddy 自动
  ACME 签证书并 80→443 跳转，附 CSP/X-Frame-Options/nosniff/HSTS 安全响应头；
  未设置 SITE_ADDRESS 退化为 :80 明文——仅限内网调试（runbook 有告警说明）
- docker-compose 中数据库不暴露公网端口；连接串经环境变量注入
- `SITE_ADDRESS` 必须显式传入 web 容器；证书状态通过持久卷保留。
- 单实例持锁连接失败即取消调度并退出，不得仅报警后继续执行；探测必须针对原持锁连接。
- 登录防爆破：IP+用户名固定窗口锁定（5 次失败/10 分钟 → 429），成功登录即重置

## 审计

- 写操作白名单强制过审计：publish/reprice/delist/zerocd/offer_sent/order.delivered/
  order.accepted/factor_reset/strategy.update/channel.credential.update/job.trigger/login.*
- price_actions 表本身即定价域的细粒度审计（含决策 jsonb）
- handler 500 响应一律脱敏为通用文案，内部错误细节只进服务端日志

## 供应链

- Go 依赖锁定 go.sum；CI 中 `govulncheck ./...`（软门控，continue-on-error，报告不阻断）
- 前端 pnpm lockfile 锁定 + package.json `packageManager` 字段钉死 pnpm 版本；仅使用 npm registry 官方源

## 测试数据红线

- testdata/fixtures 只允许脱敏载荷（假 token、测试 RSA 密钥对）
- CI 提供专用测试密钥对（仓库内生成、公开无害）；真实施行密钥永不入库
- 破坏性集成测试只接受显式 `TEST_DATABASE_URL`，数据库名须以 `test_` 开头或
  `_test` 结尾；禁止回退 `DATABASE_URL`。共享测试库的包必须串行执行。

## GitHub PAT

- 仅存 git credential store（~/.git-credentials 或远程 URL 于 .git/config，均不入库）
- 建议 fine-grained + 到期轮换；泄露立即 revoke
