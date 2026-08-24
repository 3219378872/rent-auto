# ADR-0006: JWT 会话纪元吊销体系

日期：2026-08-24
状态：已接受

## 背景

面板 JWT 仅有 HMAC+exp 校验，TTL 24h 内无法失效：登出只清前端本地 token，
token 泄漏或管理员想强制下线时无任何手段。第三轮审查列为遗留#1。
完整方案（per-jti 黑名单表+定期清理）对单管理员面板而言过重。

## 决策

**会话纪元（session epoch）版本号吊销**：

- Claims 增加 `ver`（int64 纪元），签发时取服务端当前值
  （`app_settings.key=jwt_session_epoch`，缺省 0）；
- `requireAuth` 校验 `claims.ver == 当前纪元`，不一致 → 401
  `{"code":"unauthorized","message":"token revoked"}`——与既有前端
  「401+unauthorized 即清 token 回登录页」语义天然衔接；
- 新增受保护端点 `POST /api/v1/auth/logout`：纪元 +1 + 审计 `auth.logout`；
- 单管理员语义 = 登出即「登出所有会话」（含其他浏览器/标签页），文档明示；
- 每请求一次 app_settings 主键读——单用户面板规模下成本可忽略，
  不引入缓存失效复杂度。

## 后果

- 登出/疑似泄漏可即时全量失效；无需黑名单存储与清理任务。
- 已知取舍：任何一次登出使全部设备下线（单管理员场景为预期行为）；
  未来若引入多账号/多会话需求，演进路径为 per-user epoch 或 jti 表（ADR 备忘）。
