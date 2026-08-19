# Control Plane 与 Admin BFF 详细设计

## 1. Control Plane 职责

Control Plane 是唯一配置写入口，承载：

- CollectionDefinition、Subscription、ConfigurationModel 管理；
- ReleaseTemplate 版本管理；
- ReleaseOrder 创建、读取、推进、审批、回滚和补偿；
- Audit 查询；
- 单字段敏感值受审计 reveal；
- Overlay boundary reconciler；
- outbox relay；
- Kitex CatalogAdminService、ReleaseService、AuditService、SensitiveAccessService、OperationsService。

它不提供 ConfigurationRecord 直接写 RPC；管理页面的 Add/Edit/Copy/Delete 最终必须创建 ReleaseOrder。

## 2. Control Plane 进程结构

`admin/cmd/control-plane/main.go` 是薄进程入口；唯一 composition root 位于 `admin/internal/runtime/controlplane`，注入各 application 自己声明的事务端口及其 MySQL adapter、catalog/release modules、identity policy、clock、ID generator、outbox relay 和 observability。禁止构造覆盖所有 bounded context 的 god UnitOfWork。内部 run groups 共享根 context，但错误策略不同：

- RPC server 启动失败：进程退出。
- DB 启动 capability 失败：进程退出。
- boundary reconciler/outbox relay 运行错误：记录并退避重试，ready 根据持续失败阈值降级。
- 单个请求错误：返回，不终止进程。

## 3. 元数据管理

### Collection

- 创建时完整校验字段、KeyFields、默认值和规则。
- 更新使用 expected revision。
- ENABLED Collection 有数据后禁止破坏性 schema 更新。
- DISABLED 只阻止新查询/发布，不删除历史记录。

### Subscription

- ConsumerID、Collection、IndexName 唯一。
- 索引字段变化是 metadata revision 变更，会触发 Config Server/SDK 重建。
- 禁用后 SDK 下一次收敛删除对应索引或 collection。

### ConfigurationModel

- 保存前调用 ModelCompiler；编译失败不落库。
- 更新页面/发布行为也推进绑定 collection 的 metadata revision。
- 禁用模型阻止 QueryPage 和新 ReleaseOrder，不影响历史 ReleaseOrder 详情。

### ReleaseTemplate

- 每次修改创建新 version，不 UPDATE 历史 Steps。
- 切 active template 在一个事务内完成。
- 在途 Order 只读 TemplateSnapshot。

## 4. Admin BFF 定位

Admin BFF 是浏览器与 Kitex 的协议 adapter，使用 Go 标准库 `net/http`。V1 不引入 Node 服务端或第二套领域实现。

`admin/cmd/admin-bff/main.go` 也是薄进程入口，唯一装配位于 `admin/internal/runtime/bff`。BFF runtime 只能注入 BFF application 声明的 session、identity 和 RPC ports；禁止直接装配 Catalog/Release/Access/Audit/Outbox application 或 MySQL adapter。

BFF 负责：

- OIDC/JWT 登录回调或开发 token 会话；
- Secure/HttpOnly/SameSite session cookie、CSRF；
- HTTP JSON 与 proto DTO 显式映射；
- 调用 Control Plane/Config Server；
- timeout、request id、trace context、错误 envelope；
- 对极少量页面首屏数据做并行聚合；
- 生产环境提供编译后的 SPA 静态资源，开发环境由 Vite proxy。

BFF 不负责：字段规则、状态机、权限最终判定、effective record、diff 真相或数据库访问。

BFF 的 Kitex clients 连接目标服务的 Envoy TLS/mTLS endpoint，并显式配置标准 gRPC 与 `WithGRPCTLSConfig`。目标 Kitex backend 只接受同 Pod UDS/loopback h2c；BFF 不直连或发现 backend address。mTLS 只证明调用服务身份，业务 Principal 仍由每请求短期内部 JWT 传递和验证。

## 5. HTTP 契约

OpenAPI 路径前缀 `/api/v1`，JSON 使用 lowerCamelCase，未知字段拒绝。所有写请求要求 `Content-Type: application/json`、CSRF header 和 request id。

### Session

```text
GET  /session
POST /auth/logout
```

Session 返回 subject、displayName、roles、allowedScopes 和 feature flags，不返回 token。

### Unified Operations / QueryPage

```text
GET  /models?enabled=true
POST /query-page
```

首次 ALL、后续 ONLY_DATA。BFF 不缓存 QueryPage Data；ETag 只可用于静态模型列表。

### Catalog admin

```text
GET/POST       /collections
GET/PUT        /collections/{name}
GET/POST       /subscriptions
PUT            /subscriptions/{id}
GET/POST       /models
GET/PUT        /models/{code}
GET/POST       /release-templates
GET            /release-templates/{code}/versions/{version}
```

PUT body 必须携带 expectedRevision。前端不得用 last-write-wins。

### Release

```text
GET  /release-orders
POST /release-orders
GET  /release-orders/{id}
POST /release-orders/{id}/actions
POST /release-orders/{id}/compensations
```

Create 要求 `Idempotency-Key` header。Action 要求 UUIDv4 `Idempotency-Key`，body 包含 action、expectedOrderRevision、expectedCurrentStep、comment；BFF 映射为 action_request_id。响应总是完整 ReleaseOrderDetail，并包含服务器计算的 `allowedActions`。

### Audit and diagnostics

```text
GET /audit-records
GET /diagnostics/snapshot
GET /diagnostics/collections/{name}
GET /diagnostics/outbox
POST /diagnostics/outbox/{id}/replay
```

outbox dead-letter 的人工重放使用独立 POST，只允许 PLATFORM_OPERATOR。请求携带 expectedRevision、reason 和确认短语；服务端保留原 payload/idempotency key 并写新审计，不得伪装成普通列表行内操作。

### Sensitive reveal

```text
POST /sensitive-values/reveal
```

body 包含 modelCode、scope、recordKey、fieldName、expectedRecordRevision、expectedCollectionRevision、expectedModelRevision、expectedServerEpoch、expectedSnapshotInstance、expectedSnapshotGeneration、reason 和可选 previewBucket。BFF 调 SensitiveAccessService；响应设置 `Cache-Control: no-store`，不进入 TanStack Query 长期 cache、日志或前端持久化。每次成功 reveal 已由 Control Plane 同事务写 AuditRecord。

## 6. HTTP 错误 Envelope

```json
{
  "error": {
    "code": "REVISION_CONFLICT",
    "message": "配置已被其他操作更新",
    "field": "expectedRevision",
    "resourceType": "ReleaseOrder",
    "resourceId": "...",
    "currentRevision": 42,
    "traceId": "..."
  }
}
```

HTTP 映射：InvalidArgument=400、Unauthenticated=401、PermissionDenied=403、NotFound=404、AlreadyExists=409、Aborted=409、FailedPrecondition=422、ResourceExhausted=429/413、Unavailable=503、Internal=500。

不把 Kitex/MySQL 原始错误或 stack 返回浏览器。

## 7. Timeout 与重试

- BFF 入站默认 15 秒，QueryPage 10 秒，Release action 20 秒。
- 只重试幂等 GET 和明确幂等的 POST；创建发布单仅在 Idempotency-Key 存在时最多重试 1 次。
- 不重试 4xx、Aborted 或 FailedPrecondition。
- 客户端断开必须取消下游 Kitex context。

## 8. Diff 与 ChangeDraft

ChangeDraft 只在浏览器内存在。BFF 不提供草稿存储。创建发布单前：

- 前端根据 ALL row 和动态表单生成 before/after diff，用于即时展示。
- CreateReleaseOrder 携带 expected record revision。
- Control Plane 重新读取权威记录、重新规范化并生成权威 before/after；前端 diff 不能成为写入事实。
- 若权威前态改变，返回 RevisionConflict，前端要求重新加载并重新审查。

## 9. 认证与 CSRF

- 生产：标准 OIDC Authorization Code + PKCE；BFF 使用轮换密钥签名并加密 HttpOnly session cookie，下游使用短期内部 JWT 传递 Principal。
- 开发：显式 `DEV_AUTH_ENABLED=true` 后接受静态 token，启动时打印警告；生产配置禁止开启。
- 所有非 GET/HEAD 请求校验 CSRF double-submit token 和 Origin。
- CORS 默认同源；不允许 `*` + credentials。
- 登录跳转地址和 observability URL 使用 allow-list。

## 10. 前端静态资源

生产构建将独立 `frontend/dist` artifact 复制到 BFF 镜像；Admin Go module 不通过源码相对 import 或 embed 指令耦合 Frontend 源目录。除 `/api/`、health 和静态 asset 外的 GET 回退到 `index.html` 支持 React Router；带扩展名的不存在 asset 返回 404，不回退 HTML。

静态 asset 使用内容 hash 和一年 immutable cache；`index.html` no-cache。

## 11. 测试表面

- OpenAPI request/response schema 和未知字段拒绝。
- HTTP↔proto DTO、枚举、时间、错误完整映射。
- session、CSRF、Origin、scope/role 传递。
- 客户端取消、timeout、幂等重试。
- ALL/ONLY_DATA、release action、revision conflict。
- 静态资源缓存和 SPA fallback。
- handler tests 使用 in-memory Kitex port adapter，不启动 MySQL。
