# 安全规格

## 凭证管理

| 凭证 | 存储位置 | 加密 | 展示规则 |
|---|---|---|---|
| UU token | app_settings.value_enc | AES-256-GCM(APP_MASTER_KEY) | 仅尾8位 |
| ECO partnerId | app_settings.value_plain | —（非密） | 明文 |
| ECO RSA 私钥(PKCS8) | app_settings.value_enc | AES-256-GCM | 仅显示指纹(SHA256前12位) |
| 面板管理员密码 | app_settings（bcrypt cost≥10） | bcrypt | 不可见 |
| JWT 密钥 | env JWT_SECRET（≥32B） | — | 启动时缺失则拒绝启动 |
| APP_MASTER_KEY | env（32B hex） | — | 同上 |

- 禁止：凭证写入日志、审计 detail、错误信息、API 响应
- 凭证变更必须产生 audit_log（动作+操作者+尾号指纹，不含明文）

## 传输与部署

- 面板仅监听内网/本机或置于反代(TLS)之后；后端不做 TLS 终结（部署层负责）
- docker-compose 中数据库不暴露公网端口；连接串经环境变量注入

## 审计

- 写操作白名单强制过 AuditMiddleware：publish/reprice/delist/zerocd/
  strategy.update/channel.credential.update/job.trigger/login.*
- price_actions 表本身即定价域的细粒度审计（含决策 jsonb）

## 供应链

- Go 依赖锁定 go.sum；CI 中 `govulncheck ./...`（软门控，报告不阻断）
- 前端 pnpm lockfile 锁定；仅使用 npm registry 官方源

## 测试数据红线

- testdata/fixtures 只允许脱敏载荷（假 token、测试 RSA 密钥对）
- CI 提供专用测试密钥对（仓库内生成、公开无害）；真实施行密钥永不入库

## GitHub PAT

- 仅存 git credential store（~/.git-credentials 或远程 URL 于 .git/config，均不入库）
- 建议 fine-grained + 到期轮换；泄露立即 revoke
