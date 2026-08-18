# FinConfig V1 设计文档入口

本目录是 V1 的编码依据。编码 Agent 必须从本文件开始，按下列顺序消费；原始复原提示词只提供行为背景，与本目录冲突时以本目录为准。

## 当前设计门禁

当前状态为 **IMPLEMENTATION_READY**：用户已明确要求按本文档完成全部实现。编码 Agent 按 `12-implementation-plan.md` 的垂直切片执行；遇到新的 P0 架构事实时先 grill、更新 ADR/设计和契约测试，再继续实现。

### 用户已明确确认

- V1 只实现 MySQL，不实现 PostgreSQL。
- Go RPC 框架使用官方开源版 CloudWeGo Kitex。
- 数据访问使用 GORM；数据库结构仍由版本化 migration 管理，禁止 AutoMigrate。
- 本阶段先完成规划与设计，不直接进入实现。

### 已接受的实现基线

- Kitex 使用 Protobuf proto3 与标准 gRPC/HTTP/2 wire protocol，浏览器通过 Admin BFF 的 HTTP/JSON 接入。
- Goose 负责 MySQL schema migration；`slog + OpenTelemetry Trace + Prometheus Metrics` 作为统一可观测性方案。
- Go、Kitex、GORM、MySQL 兼容矩阵和前端依赖采用下节列出的固定版本。
- 前端采用 React + TypeScript + Vite + Ant Design + React Router + TanStack Query，并使用浏览器内 ChangeDraft，不在 V1 持久化共享草稿。
- 生产认证采用 OIDC + BFF session，服务间使用 mTLS + 短期 JWT；V1 不接企业私有身份或审批 SDK。

## 已冻结的 V1 技术基线

- 后端：Go 1.26.6，单 Go module monorepo。
- RPC：官方开源 CloudWeGo Kitex `v0.16.2`，Protocol Buffers proto3，标准 gRPC/HTTP/2 wire protocol。
- 数据库：MySQL 8.4.11 LTS 为主，MySQL 8.0.46 为兼容目标；V1 不实现 PostgreSQL。
- 数据访问：GORM `v1.31.2`、`gorm.io/driver/mysql v1.6.0`、`github.com/go-sql-driver/mysql v1.10.0`；特殊锁和 revision 分配允许参数化原生 SQL。
- ID：`github.com/google/uuid v1.6.0`，领域实体使用 UUIDv7。
- Schema：Goose 版本化 SQL migration；禁止 GORM AutoMigrate。
- 可观测性：`log/slog`、OpenTelemetry Trace、Prometheus Metrics。
- 前端：Node 24 LTS、pnpm 11.4、React 19.2.8、TypeScript 5.9.3、Vite 8.x、Ant Design 6.5.2、React Router 8.3、TanStack Query 5.x。TypeScript 暂不升 6，因为 OpenAPI 类型生成器当前声明的兼容范围是 5.x。
- 浏览器接入：Admin BFF 提供 HTTP/JSON；浏览器不直接连接 Kitex。

进入 IMPLEMENTATION_READY 后，依赖必须在 `go.mod`、`go.sum` 和 `pnpm-lock.yaml` 中固定。若工具初始化时所列版本已无法解析，只允许选择同一主版本的稳定替代版本，并在变更前更新本文件和 ADR；禁止静默使用 `latest`。

### 技术名词说明

- **Goose migrations**：数据库结构版本管理工具。每次建表、加列、加索引都写成有序 SQL migration，由独立命令/部署 job 执行和记录版本；它不替代 GORM，也不允许应用启动时自动改表。
- **`slog + OpenTelemetry + Prometheus`**：三类互补的运行观测能力。`slog` 记录结构化日志；OpenTelemetry 统一生成和传播请求链路 Trace；Prometheus 采集请求数、耗时、失败数、队列积压等 Metrics。三者只负责观测，不参与配置业务一致性。

## 文档消费顺序

1. 根目录 [CONTEXT.md](../../CONTEXT.md)、[V1 executable spec](../specs/finconfig-v1.md) 与 [ADR](../adr/)；三者中的明确决策优先于早期详细设计。
2. [整体设计](./00-overall-design.md)
3. [契约与 RPC](./01-contracts-and-rpc.md)
4. [领域模型与一致性](./02-domain-and-consistency.md)
5. [规范类型参考](./A-canonical-model-reference.md)
6. [MySQL、GORM 与 migration](./03-mysql-persistence.md)
7. [配置读取服务](./04-config-server.md)
8. [Go 客户端 SDK](./05-go-client-sdk.md)
9. [QueryPage 低代码查询](./06-query-page.md)
10. [发布工作流](./07-release-workflow.md)
11. [控制面与 Admin BFF](./08-control-plane-and-admin-bff.md)
12. [管理控制台前端](./09-frontend.md)
13. [安全、可观测性与运行](./10-security-observability-operations.md)
14. [测试与交付标准](./11-testing-and-delivery.md)
15. [实施顺序与 Agent 任务包](./12-implementation-plan.md)

领域术语必须遵循仓库根目录的 [CONTEXT.md](../../CONTEXT.md)。

## 模块实现索引

编码 Agent 应按“该模块拥有的语义”决定代码归属，不按页面或 RPC 方法复制业务逻辑：

| 模块 | 详细设计 | 主要代码边界 | 模块拥有的语义 | 必须先读 |
|---|---|---|---|---|
| Contract / Transport | `01-contracts-and-rpc.md` | `api/`、`gen/`、各 transport adapter | wire DTO、错误映射、兼容规则 | 00、02 |
| Catalog | `02-domain-and-consistency.md`、`A-canonical-model-reference.md` | `internal/catalog/` | Collection、Record、Subscription、Model 与规范值 | 01 |
| MySQL adapter | `03-mysql-persistence.md` | `internal/platform/mysql/`、`db/migrations/mysql/` | GORM 映射、事务、锁、约束、revision 分配 | 02、07 |
| Distribution / Config Server | `04-config-server.md` | `internal/distribution/`、`cmd/config-server/` | 不可变 snapshot、刷新、版本、Watch | 02、03 |
| Go SDK | `05-go-client-sdk.md` | `client/` | 本地 snapshot、索引查询、Watch/poll 自愈 | 01、04 |
| PageQuery | `06-query-page.md` | `internal/pagequery/` | 模型编译、过滤、排序、分页、选项、脱敏 | 02、04 |
| Release | `07-release-workflow.md` | `internal/release/` | ReleaseOrder 聚合、模板、步骤、效果与补偿 | 02、03 |
| Control Plane / BFF | `08-control-plane-and-admin-bff.md` | `cmd/control-plane/`、`cmd/admin-bff/` | 用例装配、浏览器协议、安全边界 | 01、07、10 |
| Admin Console | `09-frontend.md` | `web/admin-console/` | 动态页面、ChangeDraft、Diff、发布交互 | 01、06、08 |
| Security / Operations | `10-security-observability-operations.md` | `internal/platform/identity/`、`internal/platform/observability/`、`deploy/` | 身份、审计、telemetry、运行生命周期 | 全部业务模块 |
| Verification / Delivery | `11-testing-and-delivery.md`、`12-implementation-plan.md` | tests、CI、examples | tracer、contract、E2E、交付门禁 | 对应目标模块 |

每个模块的公开 application 接口是主要测试表面；transport DTO、GORM struct 和 React DTO 只能在各自 adapter/UI 边界出现，不能成为跨模块共享模型。

## 输入材料与冲突处理

设计综合了三类输入：最新版 clean-room 行为复原提示词、用户逐项确认的 V1 技术约束，以及分享页《总结交互流程》的页面交互摘要。它们冲突时按以下顺序处理：

1. 用户在当前任务中明确确认的约束；
2. 本目录完成设计评审后标记为 IMPLEMENTATION_READY 的决定；
3. `docs/specs/finconfig-v1.md`、ADR 与 `CONTEXT.md`；
4. 最新版原始提示词中的行为语义；
5. 分享页中的历史页面名称和企业专用术语。

因此，最新版提示词中的 PostgreSQL、sqlc 和“不得使用 Kitex/GORM”不进入 V1；分享页中的 PSM、PPE、单机房等只保留其通用交互意图，分别抽象为 Consumer、可配置 Scope 和 ReleaseTemplate step，不进入代码枚举或页面硬编码。

## 编码 Agent 工作协议

每个任务开始前必须：

1. 阅读整体设计和目标模块设计。
2. 读取目标模块引用的上游契约，不通过猜测补字段。
3. 检查当前 worktree，保留用户已有改动。
4. 先写模块接口级测试，再实现模块内部细节。
5. 只修改当前任务包声明的目录；跨模块契约变更必须先更新设计文档。

每个任务完成时必须：

1. 运行目标模块测试及受影响的契约测试。
2. 报告实际行为、测试命令和仍未实现的后续任务。
3. 不提交 TODO、空 handler、伪实现或用内存假数据冒充生产 adapter。
4. 不重新定义同名 DTO、领域枚举或错误码。

## 明确排除

- PostgreSQL、sqlc、Thrift、Kitex 私有 payload/TTHeader、企业服务发现。
- GORM AutoMigrate、GORM domain model、GORM hooks 承载领域副作用。
- 任意 SQL/表名/JOIN/脚本驱动的低代码查询。
- 浏览器直连 Kitex、前端直接持有数据库语义。
- 第一版的多租户、跨地域多主写、外部审批厂商和消息队列强依赖。
- 直接修改生产 ConfigurationRecord；所有记录变更必须形成 ReleaseOrder。
- 对已成功 ReleaseOrder 原地回滚；成功后只能创建 CompensatingRelease。

## 设计中的固定默认值

这些默认值用于消除原提示词中的开放选择，编码 Agent 不需要再次询问：

- Scope 字符串 trim 后大小写敏感；所有标识使用 ASCII 稳定编码。
- 页大小超过模型上限返回 `InvalidArgument`，不静默截断。
- `CLOSED_RANGE` 为闭区间，`OPEN_RANGE` 为开区间；单边界沿用相同包含性。
- JSON 字段只允许 `EXACT/IN/NOT_IN`，不得作为默认排序字段。
- QueryPage snapshot identity 变化时前端重置到第一页；Generation 不跨 ServerInstanceID 比较，V1 不承诺跨 snapshot identity 的连续分页一致性。
- `CollectionDefinition` 是数据契约真相；`ConfigurationModel` 是页面、查询和发布行为真相。重复属性必须在模型编译时完全一致。
- Overlay 时间边界由后台协调器转换为有 revision 的显式激活/失效；不允许仅凭墙钟时间无版本改变可见结果。
- 所有新建领域实体 ID 使用 UUIDv7 小写字符串；ReleaseNumber 固定为 `REL-` + UUIDv7。浏览器幂等键使用 `crypto.randomUUID()` 生成 UUIDv4。
