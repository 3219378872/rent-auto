# 2026-08-24 遗留清欠轮（第四轮迭代）

## 范围

第三轮审查遗留清单的优先项落地：JWT 吊销体系 / 哨兵错误语义统一 /
openapi.yaml 全量回写，外加两项低风险快赢（Refresh 锁内网络 IO、分页上限）。

## 交付清单

| 项 | 内容 | 验证 |
|---|---|---|
| JWT 会话纪元吊销（ADR-0006） | Claims 增加 `ver`；requireAuth 校验纪元不一致即 401 unauthorized；新增 POST /auth/logout（bump app_settings.jwt_session_epoch + 审计）；前端退出先吊销再清本地；单管理员语义=登出所有会话 | TestLogoutRevokesTokens（登录→登出→旧 token 401→重新登录可用） |
| 哨兵统一·PartialError | Unwrap()→ErrPartialFailure，跨渠道 errors.Is 判定一致 | TestPartialErrorUnwrapsToSentinel |
| 哨兵统一·ECO 凭据失效 | checkEnv 将 4004(IP 白名单)/5005(身份无效) 映射 ErrAuthExpired——调度器风控冷却分支得以介入 | TestCredentialFailuresMapToAuthExpired |
| Refresh 锁外构建 | 三段式：短锁读凭据→无锁平台验证往返→原子安装/丢弃；UU 慢响应不再阻塞全部 Get/All 读取（~20s 卡顿根因）；buildUUAdapter 删除 | channels 集成套件 -race 全绿（含半注册反例回归） |
| 分页上限 | uu/eco 六处分页循环加 maxListPages=500（staticcheck QF1006 提升进循环条件）；服务器持续回满页退化为截断快照而非限频慢无限循环 | platform 四包测试全绿 |
| openapi.yaml v0.3.0 | 3/19 → **20/20** 全路由契约：请求/响应 schema、分页参数组件、错误响应组件、UUSmsResponse 三模式、Dashboard 结构 | yaml.safe_load 解析通过，路径集与 server.go Routes() 一一对应 |
| security-spec 同步 | 新增「会话与吊销」节（纪元机制+登录限流清扫） | 文档评审 |

## 执行的命令与结果

```
make gate   # GATE PASSED（unit+integration -race / cover-gate / migrate-check / 前端四件套）
./scripts/coverage-gate.sh backend 70
# pricing 91 / platform 100 / uu 77 / eco 78 / steam 76 / recon 80↑ /
# analytics 81 / auth 90 / secrets 79 / config 76 —— 全部 ≥70
```

## 已知偏差与理由

- 登出为全局吊销（非 per-session）：单管理员产品语义下的有意取舍，
  多会话需求的演进路径已记入 ADR-0006 备忘
- PublishLease 未引入部分失败哨兵（保持 results 数组语义）：
  现有调用方逐项读 Success 标志，贸然改返回值契约风险大于收益；
  哨兵不对称已在 adapter.go 注释与第三轮遗留清单中备案
- ECO 5004（验签失败）未映射 ErrAuthExpired：可能是本地签名 bug 而非凭据失效，
  归入 generic 错误观察

## 移交后续轮次（更新后遗留）

1. X-Real-IP 可信代理判定 + loginLimiter per-IP 二级上限
2. 哨兵：PublishLease 部分失败哨兵化（需调用方同步评估）
3. 空货架同步熔断；DB CHECK 约束迁移；foldOrderEvents UU 渠道待真机字段确认
4. 前端缺口：仪表盘告警条/Listings 决策依据列/模板级策略 UI/黑名单管理/审计时间过滤
5. CI 强化：golangci 版本锁定/goinstall 缓存/react-hooks 插件/Vite7+Vitest3 升级/
   workflow concurrency+timeout+permissions/pre-push 钩子接线文档化
6. api-notes 回写：Steam 刷新节奏 Go 版简化偏离；两平台 HTTP 状态码处理约定成节
