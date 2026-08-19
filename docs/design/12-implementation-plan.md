# 垂直实施顺序与 Agent 任务包

## 0. 启动门禁

编码 Agent 首先读取 `docs/design/README.md` 的当前设计门禁。状态为 `ARCHITECTURE_MIGRATION_REQUIRED` 时，只执行 `00a-multi-module-monorepo.md` 的迁移批次，不得继续向旧单 module 路径增加功能。状态恢复为 `IMPLEMENTATION_READY` 后才继续下面的垂直切片。

## 1. 执行协议

实现按端到端垂直切片推进，不先水平完成所有 domain/schema/server。每个切片都必须包含：一个失败的 tracer acceptance test、纯领域测试、application seam 测试、真实 MySQL contract、Kitex/OpenAPI contract，以及适用时的一条 Playwright journey。Exit Criteria 未满足不得开始依赖切片。

票据真相位于 `.scratch/finconfig-v1/issues/`。本文件只给稳定顺序；票据保存具体状态、依赖、验收和验证命令。

## 2. S0 — Contract/toolchain spine

固定 Go/pnpm 工具链、proto/OpenAPI、standard gRPC Kitex generation、independent grpc-go smoke、Envoy TLS 边界、MySQL Goose harness、认证 adapter seam、CI skeleton。Exit：deterministic generation；经 TLS terminator 的 unary/Watch/status-detail gRPC smoke；MySQL 8.4/8.0 migration harness可运行；空项目 lint/test/build 通过。生产 Envoy mTLS 负向矩阵在 S9 compose 中完成。

## 3. S1 — Base-only walking skeleton

从 Collection+Model 创建，经 BASE_FINAL Release 写目标 Environment base，到 Config FULL snapshot、QueryPage、SDK Query、BFF 和最小动态 UI。Exit：production/staging 隔离的真实 MySQL+RPC+browser E2E 通过；无直接 record CRUD。

## 4. S2 — Concurrency and durability

加入三类 revision、record/collection optimistic checks、action idempotency、change log、audit、outbox、RefreshHint、version poll 和 Watch。Exit：并发/重试/信号丢失 tracer 均无重复效果并最终收敛。

## 5. S3 — Manual approval workflow

交付 create/submit/approve/reject/execute/advance/complete、allowedActions、角色和 self-approval。Exit：完整人工审批浏览器 journey、所有状态×动作表、旧 EntityRevision 与重复 action ID 测试通过。

## 6. S4 — Scope Overlay

交付 effective evaluator、OVERLAY_FINAL/OVERLAY_APPLY、Scope 查询、精确 Overlay effect/rollback。Exit：两个 Scope 得到不同有效值，rollback 恢复 exact previous rule。

## 7. S5 — Percentage rollout

交付 bucket/ranges、两个 ClientID、PreviewBucket、COMPARE、BASE_FINAL promotion 和 exact cleanup。Exit：固定协议向量、单调扩容、base promotion/rollback E2E 通过。

## 8. S6 — Dynamic UI, options and sensitive access

交付完整 interaction metadata、dynamic query/table/form、COLLECTION dependency group、ResolvedOptionSet 发布校验、sensitive reveal 与 no-store/60秒清除。Exit：不含 model-specific UI 分支；依赖组故障保留整组旧值；stale reveal/audit failure 不泄露。

## 9. S7 — Rollback and compensating release

交付全部 versioned StepEffect、IN_PROGRESS inverse compensation、SUCCEEDED CreateCompensatingRelease。Exit：base+removed overlay 精确恢复；第三方偏移阻止自动 compensation。

## 10. S8 — Metadata, diagnostics and dead-letter operations

交付完整 Catalog/Template 管理、模型预检、Audit、snapshot diagnostics、Outbox dead-letter/replay UI。Exit：权限、revision conflict、只读诊断、LeaseRevision replay contract 与 Playwright 通过。

## 11. S9 — Operations hardening

交付 slog/OTel/Prometheus、OIDC/session/CSRF/mTLS/JWT、shutdown、Docker/compose、seed/examples、容量/故障注入、MySQL 双版本、race/Playwright 矩阵。Exit：`11-testing-and-delivery.md` Definition of Done 全部满足，无敏感/高基数 telemetry。

## 12. Final verification and review

在固定 git base 上分别执行 Standards review 与 Spec review，修复后重跑全矩阵；最后按真实命令、配置、部署和限制更新 README/运行文档。不得以 TODO、空 handler、伪 adapter 或降低测试替代未完成范围。

## 13. 变更控制

发现设计无法实现时，先提交最小复现和受影响不变量，再修改 spec/ADR/contract test，最后改实现。禁止通过透传接口、跳过 ReleaseOrder、关闭校验或 handler 特例临时绕过。
