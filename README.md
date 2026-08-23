# rent-auto — Steam 饰品自动化租赁系统（管理面板 + 双渠道自动化）

基于 [Steamauto](https://github.com/Steamauto/Steamauto) 的行为规格，以 Golang 后端 + React 前端
重写的**悠悠有品(UU) + ECOSteam(ECO) 双渠道全量租赁自动化系统**。

- 自动上架 / 改价 / 0CD 转租，长期无人值守运行
- 跨平台价格基准：UU 公开租赁行情 + ECO 全量在售价锚点 + 两渠道已实现收益反馈
- 收益最大化定价引擎（分渠道差异化决策）
- 统计：总资产、总收入、年化收益率、分类成本/收益率

## 文档导航

见 [AGENTS.md](AGENTS.md)（面向 Agent 的知识索引）。
人类读者入口：[docs/knowledge/intent/vision.md](docs/knowledge/intent/vision.md)。

## 开发

```bash
make gate        # 全量门控：fmt/lint/vet/build/test/前端全套
make test        # 单元测试
make worktree-new NAME=feat-xxx   # 开始一个任务迭代
```

## 安全

任何 token、私钥、密码不得入库。平台凭证仅存于数据库加密字段或环境变量，
主密钥经 `APP_MASTER_KEY` 注入。GitHub PAT 仅配置于 git credential，不写入仓库文件。
