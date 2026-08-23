# rent-auto — Steam 饰品自动化租赁系统（管理面板 + 双渠道自动化）

基于 [Steamauto](https://github.com/Steamauto/Steamauto) 的行为规格，以 Golang 后端 + React 前端
重写的**悠悠有品(UU) + ECOSteam(ECO) 双渠道全量租赁自动化系统**。

- 自动上架 / 改价 / 0CD 转租，长期无人值守运行
- 自动发货闭环：UU 待发货代发、ECO 四步交付编排，Steam 零成本报价自动接受（含手机确认器）
- 跨平台价格基准：UU 公开租赁行情 + ECO 全量在售价锚点 + 两渠道已实现收益反馈
- 收益最大化定价引擎：基线采样 → 反馈控制器（rent_success/stale/bought_out 因子）→ 渠道分化决策
- 安全基线：凭证 AES-256-GCM 落库、登录防爆破、全写操作审计、Caddy 自动 HTTPS 与安全响应头
- 统计：总资产、总收入（口径 B 净收益）、年化收益率、分类成本/收益率

## 文档导航

见 [AGENTS.md](AGENTS.md)（面向 Agent 的知识索引）。
人类读者入口：[docs/knowledge/intent/vision.md](docs/knowledge/intent/vision.md)。

## 开发

```bash
make gate        # 全量门控：fmt/lint/vet/build/test/前端全套
make test        # 单元测试
make worktree-new NAME=feat-xxx   # 开始一个任务迭代
```

## 部署

生产环境在 `deploy/.env` 设置 `SITE_ADDRESS=<域名>`，Caddy 自动签发 Let's Encrypt
证书并在 80 端口做 HTTPS 跳转（同时注入 CSP/HSTS 等安全头）。未设置时退化为 :80
明文，仅限内网调试。详见 [docs/knowledge/impl/release-runbook.md](docs/knowledge/impl/release-runbook.md)。

## 安全

任何 token、私钥、密码不得入库。平台凭证仅存于数据库加密字段或环境变量，
主密钥经 `APP_MASTER_KEY` 注入。GitHub PAT 仅配置于 git credential，不写入仓库文件。
