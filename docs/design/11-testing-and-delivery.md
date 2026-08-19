# 测试与交付标准

## 1. 测试原则

- 模块接口是主要测试表面，断言可观察结果和稳定错误，不断言私有 helper/GORM 调用顺序。
- 纯算法使用 table/property/fuzz tests。
- MySQL 行为使用真实 MySQL 8.4.11/8.0.46 Testcontainers 或 compose；禁止 SQLite 代替。
- transport 使用 contract tests；E2E 只覆盖跨部署关键路径，不重复所有边界。
- 时间、ID、Clock、网络错误通过注入 adapter 控制；禁止 sleep 驱动脆弱测试。

## 2. 测试层次

### 2.1 Domain tests

- 标量/RecordKey/索引规范编码、无碰撞和 fuzz。
- 字段、记录、模型编译、option source。
- Overlay scope、bucket、时间 marker 和 ADD/MODIFY/DELETE。
- 稳定序列化、SHA-256。
- ReleaseTemplate、rollout 策略互斥、状态转换、effect/compensation。

### 2.2 Module interface tests

- CatalogCommands 使用 in-memory persistence adapter 验证行为。
- SnapshotManager 使用 deterministic repository fake 验证刷新和原子发布。
- PageQuerier 使用 immutable snapshot fixtures。
- ReleaseCommands 使用 transactional in-memory adapter，验证聚合结果和 outbox/audit 意图。
- SDK 使用 in-process ConfigService adapter。

in-memory adapter 必须模拟必要的唯一性/事务回滚，但不能替代 MySQL contract tests。

### 2.3 MySQL integration tests

同一 suite 分别运行 MySQL 8.4.11 与 8.0.46：

- Goose 空库 up/down/up 和前版本升级。
- JSON、DATETIME(6)、NULL、collation、CHECK/FK/UNIQUE。
- GORM explicit mapping 往返。
- revision allocator 并发和回滚。
- expected revision update、FOR UPDATE、deadlock retry。
- active conflict key、active template slot。
- release 原子写入与故障注入。
- change log cursor、final-state reread。
- outbox SKIP LOCKED、lease、retry、dead-letter。
- overlay boundary 多 worker 一次处理。

### 2.4 Transport contract tests

- Buf lint/breaking 和 deterministic generation。
- Kitex 标准 gRPC unary/stream interoperability；快速测试 TLS terminator 强制 TLS/ALPN 后以 h2c 转发，生产 Envoy 配置另做静态校验和 compose mTLS E2E。
- proto↔domain 双向映射、未知枚举、timestamps。
- gRPC error status/detail。
- Sensitive reveal 成功必须伴随 audit，审计失败不得返回明文。
- OpenAPI validation、HTTP mapping、CSRF/session。

### 2.5 Frontend tests

- Vitest/Testing Library：动态字段、draft/diff/wizard、actions/errors。
- MSW fixture 必须通过 OpenAPI schema。
- Playwright：关键用户旅程和无障碍 smoke。

## 3. E2E 场景

compose 启动 MySQL、Control Plane、Config Server、BFF/Console 和两个不同 ClientID 的 SDK test clients：

1. migration + seed demo Collection/Record/Subscription/Model/Template。
2. Config Server FULL，Client A/B Start，QueryOne/Many/All 一致。
3. QueryPage ALL 返回动态字段、STATIC/COLLECTION options 和数据；ONLY_DATA 筛选/分页。
4. UI/API 创建 ADD ReleaseOrder，审批，Overlay Apply；对应 Scope 可见，其他 Scope 不可见。
5. PERCENT_ROLLOUT 只命中指定 bucket 的 Client。
6. BASE_APPLY、COMPARE、COMPLETE，Overlay 正确清理。
7. MODIFY、DELETE 和 500 item 上限内 batch 原子发布。
8. IN_PROGRESS Rollback 恢复；SUCCEEDED 创建 CompensatingRelease。
9. 丢弃 RefreshHint/Watch，version poll 最终收敛。
10. 单 collection refresh 注入失败，旧集合/cursor 保留，其他集合成功。
11. 慢 Watch、panic callback、BFF 客户端取消不影响主路径。
12. 元数据 revision conflict、release concurrent action 只有一个成功。
13. 未来 Overlay 边界由 reconciler 产生 revision 后生效/失效。
14. restart Control Plane/Config Server/Client，已提交事实与 last-known-good 恢复。
15. Sensitive 字段默认掩码，单字段 reveal 可审计且 no-store；dead-letter outbox 受控重放保持幂等。

## 4. Seed demo

seed 命令而非生产 migration 创建：

- `service_routes` Collection：routeCode、region、enabled、priority、endpoint、updatedAt。
- 一条 base record 和 default Environment version。
- `route_status_options` Collection，供 COLLECTION OptionSource。
- demo Consumer 的 ONE_TO_ONE 与 ONE_TO_MANY Subscription。
- `service-route` Model，含全部常用 UIControl、QueryPageDefinition、STATIC/COLLECTION options。
- `standard-release` Template：MANUAL_REVIEW → OVERLAY_APPLY → BASE_APPLY → COMPARE → COMPLETE。
- `percent-release` Template：MANUAL_REVIEW → 多个 PERCENT_ROLLOUT → COMPARE → BASE_APPLY → COMPLETE。

不预置 ReleaseOrder、ChangeLog、Audit 业务动作和 Outbox；它们由 demo 流程真实产生。

## 5. CI 阶段

CI 固定顺序：

```text
format/lint
-> proto/openapi generation check
-> Go unit + fuzz smoke + race
-> frontend lint/typecheck/unit/build
-> MySQL 8.4 integration
-> MySQL 8.0 compatibility
-> E2E + Playwright
-> dependency/license/secret/image scan
```

Go 测试按 module 独立运行，随后再运行 workspace/compose 验收。根目录没有 `go.mod`，不得用根 `go test ./...` 假装覆盖全部 module；CI 必须显式枚举 Contracts、Platform、Admin、Server 和 Client SDK。每个支撑 module 先独立发布可解析 tag，依赖它的产品在后续 commit 更新精确版本，并从该 dependent commit 开始用 `GOWORK=off` 验证依赖闭合；`go.work` 测试不能代替这一门禁。依赖方向检查拒绝产品 module 之间的 Go import，并拒绝 Admin BFF 绕过自身 RPC port 导入 Control Plane 领域/application/infrastructure。

命令由 Makefile 封装：

```bash
make generate-check
make lint
make test
make test-race
make test-integration-mysql84
make test-integration-mysql80
make test-e2e
make web-test
make web-e2e
make build
```

生成检查必须在干净 worktree 重新生成并断言无 diff。

## 6. 性能验收

在固定 fixture 上记录基线，不把绝对数字写成跨硬件 SLA：

- SDK QueryOne/Many 无网络、无意外分配回归。
- 100k record snapshot build 的耗时/峰值内存。
- QueryPage 100k candidate、20 条件、200 page 的阶段耗时。
- 500 item 发布事务的锁持有时间。
- 1,000 Watch clients 的广播和慢 consumer 隔离。

PR 只对相对基线设置合理回归阈值；production SLO 在部署环境另行定义。

## 7. 故障注入

必须可测试：MySQL deadlock/timeout、事务中途失败、outbox 投递失败、Config Server refresh 某集合失败、坏压缩 payload、Watch 断线、callback panic、BFF 下游 timeout、Overlay reconciler 重复领取。

验证重点是“不产生部分事实”和“保留 last-known-good”，不是仅验证返回 error。

## 8. 交付物

- 完整 Go/前端源码、固定依赖和生成配置。
- proto/OpenAPI 和已提交生成代码。
- MySQL Goose migrations、seed command。
- Config Server、Control Plane、Admin BFF Dockerfile。
- docker-compose 和示例 YAML/env。
- Go SDK 示例：Start、查询、Decode、Subscribe、多 region。
- CLI 示例：创建、审批、执行、推进、回滚、补偿、详情。
- QueryPage 示例：ALL、ONLY_DATA、options、错误。
- `/healthz`、`/readyz`、`/metrics`。
- README、设计文档、必要 ADR 和运维手册。

## 9. Definition of Done

某模块只有同时满足以下条件才算完成：

1. 对外接口和错误语义完整，不存在 TODO/空 handler。
2. 模块接口测试通过，必要 MySQL/transport contract tests 通过。
3. context cancellation、timeout、关闭和可观测性已实现。
4. 无领域对象泄漏到 proto/GORM/React DTO。
5. 无敏感值日志、任意 SQL、AutoMigrate、全局可变单例。
6. 文档与实际命令一致，新开发者可从空库启动 demo。

整个 V1 完成还要求所有 CI、MySQL 8.4.11/8.0.46、race、E2E 和 Playwright 通过。
