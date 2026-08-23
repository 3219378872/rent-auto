# 仓库结构与代码归属

```
rent-auto/
├── AGENTS.md                  # Agent 索引入口（改结构必更新本文件与 knowledge 地图）
├── Makefile                   # 所有门控/开发命令唯一入口
├── docs/knowledge/            # 五层知识库（见 AGENTS.md 地图）
├── backend/
│   ├── cmd/server/main.go     # 组装根：config→store→adapters→scheduler→api
│   ├── cmd/migrate/main.go    # 迁移 CLI（up/down/status，嵌入FS）
│   └── internal/
│       ├── config/            # 环境变量解析与校验（无 viper，纯 stdlib+env）
│       ├── api/               # HTTP：路由/中间件/handlers；禁止业务规则入内
│       ├── domain/            # 跨模块共享类型与枚举（零外部依赖）
│       ├── platform/
│       │   ├── adapter.go     # ChannelAdapter 接口 + Capabilities
│       │   ├── uu/            # UU 客户端（crypto/login/endpoints/models）
│       │   └── eco/           # ECO 客户端（sign/endpoints/models）
│       ├── bench/             # 价格基准中心：registry/anchor/snapshot查询
│       ├── pricing/           # 定价引擎（纯函数域，网络依赖通过接口注入）
│       ├── scheduler/         # 任务定义/调度循环/限频器/dry-run
│       ├── recon/             # Reconciler 差异计算
│       ├── analytics/         # 收益记账与 rollup
│       ├── audit/             # 审计服务
│       └── store/             # pgx 查询层 + migrate 嵌入；接口在 store/store.go
│   └── migrations/NNNN_*.up.sql / .down.sql
├── frontend/                  # React+Vite+TS（src/pages 与 API 一一对应）
├── deploy/docker-compose.yml  # postgres + backend + caddy
├── scripts/                   # fixture 生成等辅助脚本（Python 参考件调用放这里）
└── .github/workflows/ci.yml
```

## 归属规则

1. 平台协议细节 → 只进 `platform/<ch>/`；业务判断禁止写进适配器
2. 跨渠道通用概念（Listing/Decision/统一状态机）→ `domain`
3. SQL 只存在于 `store/`；handler 不直接持有 *pgxpool
4. 测试文件与被测包同目录；跨包契约测试放 `*_contract_test.go`
5. testdata/fixtures 与被测包同级，脱敏载荷

## 依赖方向（lint 强制心智检查）

`api → scheduler/analytics/recon/bench → pricing/platform → store → domain`
禁止反向引用；pricing 不得 import store/api。
