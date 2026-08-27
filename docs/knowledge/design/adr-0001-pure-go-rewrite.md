# ADR-0001 — 纯 Go 重写平台对接（弃用 Python sidecar）

日期：2026-08-23 ｜ 状态：已接受

## 背景
上游 Steamauto 为 Python 实现。复用路径有二：(A) Python 引擎作为 sidecar 进程由 Go 驱动；(B) 以 Python 为行为规格，Go 原生重写所需 API 客户端。

## 决策
选 B。理由：
1. 单二进制部署是"长期无人值守"的最小运维面（无 Python 运行时/venv/pip 版本地狱）
2. 加密方案简单且可验证：UU=AES-128-ECB+RSA-PKCS1v15（stdlib 可实现）；ECO=SHA256withRSA（stdlib）
3. 我们需要的租赁域端点约 15~20 个，远小于全量移植成本
4. sidecar 方案的进程管理/日志解析/配置同步复杂度高于重写

## 后果
- 必须建立 fixture 交叉验证机制（用原 Python 实现生成密文/签名样例，Go 解码回归）——见 testing-strategy.md
- 未文档化平台行为存在踩坑风险——以 design/platform-*-api-notes.md 承接逆向知识
- 上游后续新功能需手动跟随

> 注：本文件初稿曾将「PostgreSQL 唯一存储」（现 ADR-0007）与「双通道一等公民
> 适配层」（现 ADR-0008）两条决策并入文内，2026-08-27 round10 拆分为独立文件，
> 撞号与拆分说明见 architecture.md「ADR 索引」。
