# 契约与 RPC 详细设计

## 1. 当前事实与第 0 阶段

当前仓库没有 proto、生成代码、SDK interface 或 HTTP 契约。原始提示词中的“接口已经存在且不可修改”不成立，因此实施必须先完成 Contract Freeze：

1. 创建 proto3 文件和 Buf/生成工具配置。
2. 生成 Kitex Go 代码并提交生成产物。
3. 为编码、默认值、错误码、流式行为编写 contract tests。
4. 创建 Admin BFF OpenAPI，并从契约生成或校验 TypeScript DTO。
5. Contract Freeze 后，后续 Agent 不得未经设计变更修改字段号、枚举数值和方法语义。

Proto 字段只追加、不复用已删除字段号；删除字段必须 `reserved`。所有时间使用 `google.protobuf.Timestamp`，duration 使用 `google.protobuf.Duration`。

## 2. RPC 技术约束

- 使用官方开源 Kitex 与 Protobuf proto3。
- wire protocol 固定为标准 gRPC over HTTP/2，禁止 Kitex PurePayload、TTHeader 和私有 codec。
- 所有 Kitex client composition root 显式选择标准 gRPC transport；生产 client 还必须设置 `WithGRPCTLSConfig` 并连接目标 Envoy。不得依赖 streaming 或 generator 默认；用独立 grpc-go client 验收 unary、Watch 和 status details。
- 服务地址来自静态配置、DNS 或 Kubernetes Service。
- 所有 unary RPC 必须透传 deadline/cancellation。
- Watch 使用 server streaming；server 必须感知客户端取消并释放队列。
- 生产可达地址是 Envoy sidecar 的 TLS/mTLS gRPC listener；Envoy 以同 Pod UDS（首选）或 loopback h2c 转发给 Kitex server。Kitex backend 不得暴露 Service/host port。
- 开发模式只允许对 loopback/UDS backend 显式关闭 TLS，不能形成跨网络明文路径。
- Kitex handler 只负责认证结果读取、DTO 映射、调用模块和错误映射，不编排领域细节。

## 3. Proto 包与服务

### 3.1 `finconfig.config.v1.ConfigService`

面向 Go SDK 的读取面接口：

```proto
service ConfigService {
  rpc GetSnapshot(GetSnapshotRequest) returns (GetSnapshotResponse);
  rpc DiffVersions(DiffVersionsRequest) returns (DiffVersionsResponse);
  rpc GetCollections(GetCollectionsRequest) returns (GetCollectionsResponse);
  rpc Watch(WatchRequest) returns (stream UpdateEvent);
}
```

语义：

- `GetSnapshot`：按 `consumer_id + client_id + scope` 返回该 Consumer 有权读取的集合；允许传 known versions，只返回需要替换的集合和明确的删除集合。
- `DiffVersions`：比较客户端 version view 与当前 snapshot，返回互斥、去重、稳定排序的 ADD/MODIFY/DELETE 集合。
- `GetCollections`：按明确集合名拉取内容，用于增量刷新；不得绕过 Subscription 权限。
- `Watch`：建立后先返回一条当前 generation/revision 水位事件，之后发送合并后的 UpdateEvent。事件不是数据本身。

`GetSnapshotResponse` 必须包含：server_epoch、server_instance_id、snapshot_instance、snapshot_generation、published_at、scope、subscriptions、collection payloads、版本和每个集合的 change cursor。epoch 变化要求客户端 FULL；generation 不跨 instance 比较大小。压缩 payload 具有显式 `format_version`、`codec` 和对当前 Client bucket 有效视图计算的 `effective_digest`。

### 3.2 `finconfig.config.v1.PageQueryService`

面向 Admin BFF 的模型驱动读取接口：

```proto
service PageQueryService {
  rpc QueryPage(QueryPageRequest) returns (QueryPageResponse);
}
```

`QueryPageRequest` 包含 ModelCode、Scope、QueryType、Conditions 和 PageRequest。`desc` 若为兼容字段，只用于调用方展示，不参与权限、查询、缓存或 metric label。

请求还包含可选 `preview_bucket`，只允许拥有发布诊断权限的调用方使用，取值 0..99；普通管理查询不传该字段。

`QueryPageResponse` 包含：

- model code/name；
- ALL 时的 interaction fields、resolved options、enabled release types；
- 重复的结构化 `PageRow`：`record_key`、`record_revision`、`map<string,string> values` 和 `repeated string masked_fields`；BFF 再转换为 JSON，proto 中不得尝试声明非法的 `repeated map`；
- page number/size、total number/pages；
- stable projection_fields、完整 interaction metadata；
- server epoch/instance/snapshot instance/generation、model revision、collection revision。

### 3.3 `finconfig.config.v1.RefreshService`

只允许 Control Plane 身份调用：

```proto
service RefreshService {
  rpc Notify(RefreshHintRequest) returns (RefreshHintResponse);
}
```

成功响应表示 Hint 已去重并进入有界 refresh queue，同时返回当前 generation/revision 水位；不承诺已完成刷新。队列满返回 ResourceExhausted，outbox relay 负责重试，VERSION_POLL 负责最终兜底。

### 3.4 `finconfig.config.v1.DiagnosticsService`

```proto
service DiagnosticsService {
  rpc GetSnapshotStatus(GetSnapshotStatusRequest) returns (GetSnapshotStatusResponse);
  rpc GetCollectionStatus(GetCollectionStatusRequest) returns (GetCollectionStatusResponse);
}
```

只返回 generation、revision、cursor、digest、refresh 结果、容量和失败摘要，不返回配置正文。需要 PLATFORM_OPERATOR 或 AUDITOR 权限。

### 3.5 `finconfig.control.v1.CatalogAdminService`

只提供管理元数据，不提供 ConfigurationRecord 的直接写接口：

```proto
service CatalogAdminService {
  rpc CreateCollection(CreateCollectionRequest) returns (CollectionResponse);
  rpc UpdateCollection(UpdateCollectionRequest) returns (CollectionResponse);
  rpc GetCollection(GetCollectionRequest) returns (CollectionResponse);
  rpc ListCollections(ListCollectionsRequest) returns (ListCollectionsResponse);

  rpc CreateSubscription(CreateSubscriptionRequest) returns (SubscriptionResponse);
  rpc UpdateSubscription(UpdateSubscriptionRequest) returns (SubscriptionResponse);
  rpc ListSubscriptions(ListSubscriptionsRequest) returns (ListSubscriptionsResponse);

  rpc CreateModel(CreateModelRequest) returns (ModelResponse);
  rpc UpdateModel(UpdateModelRequest) returns (ModelResponse);
  rpc GetModel(GetModelRequest) returns (ModelResponse);
  rpc ListModels(ListModelsRequest) returns (ListModelsResponse);

  rpc CreateReleaseTemplate(CreateReleaseTemplateRequest) returns (ReleaseTemplateResponse);
  rpc GetReleaseTemplate(GetReleaseTemplateRequest) returns (ReleaseTemplateResponse);
  rpc ListReleaseTemplates(ListReleaseTemplatesRequest) returns (ListReleaseTemplatesResponse);
}
```

Collection/Subscription/Model 更新必须携带 `expected_revision`。ReleaseTemplate 不原地更新；“修改”实际创建新 version，并切换 active slot。

### 3.6 `finconfig.control.v1.ReleaseService`

```proto
service ReleaseService {
  rpc CreateReleaseOrder(CreateReleaseOrderRequest) returns (ReleaseOrderDetailResponse);
  rpc GetReleaseOrder(GetReleaseOrderRequest) returns (ReleaseOrderDetailResponse);
  rpc ListReleaseOrders(ListReleaseOrdersRequest) returns (ListReleaseOrdersResponse);
  rpc ActOnReleaseOrder(ActOnReleaseOrderRequest) returns (ReleaseOrderDetailResponse);
  rpc CreateCompensatingRelease(CreateCompensatingReleaseRequest) returns (ReleaseOrderDetailResponse);
}
```

`CreateReleaseOrderRequest` 必须包含：idempotency key、model code、release type、scope、description、items，以及每个目标的 base_before、effective_before、after、expected record revision 和 expected collection revision；可选 effective_from/effective_until 受 Template scheduling policy 约束。ADD 的 expected record revision 为 0；MODIFY/DELETE 必须大于 0。MODIFY 可携带 `preserve_sensitive_fields`，由服务端从权威前态填入完整 After；调用方不得用掩码字符串充当真实值。

`ActOnReleaseOrderRequest` 包含 order id、UUIDv4 action_request_id、expected order revision、action、comment；步骤 code 只用于防止客户端操作陈旧页面，服务端仍以聚合根 CurrentStep 为权威。

### 3.7 `finconfig.control.v1.AuditService`

```proto
service AuditService {
  rpc ListAuditRecords(ListAuditRecordsRequest) returns (ListAuditRecordsResponse);
}
```

只允许按受限字段、时间范围和分页查询；不提供修改或删除 RPC。

### 3.8 `finconfig.control.v1.SensitiveAccessService`

```proto
service SensitiveAccessService {
  rpc RevealField(RevealFieldRequest) returns (RevealFieldResponse);
}
```

请求包含 model code、scope、record key、field name、expected record revision、expected collection revision、expected model revision、expected server epoch、expected snapshot instance/generation、reason 和可选 preview bucket。只允许读取标记为 Sensitive 的单个字段。Control Plane 在一个 REPEATABLE READ 事务中加载 base、active overlay 与 model，复用 Distribution evaluator，校验全部水位和 SENSITIVE_VIEWER，并写成功 AuditRecord；提交后才返回明文，审计失败不返回值。响应不得被 BFF 或浏览器缓存。

### 3.9 `finconfig.control.v1.OperationsService`

```proto
service OperationsService {
  rpc ListOutboxEvents(ListOutboxEventsRequest) returns (ListOutboxEventsResponse);
  rpc ReplayOutboxEvent(ReplayOutboxEventRequest) returns (OutboxEventResponse);
}
```

Replay 只允许 DEAD_LETTER，要求 expected event revision、reason 和 PLATFORM_OPERATOR；操作写 AuditRecord，保留原 event id/payload/idempotency key，只重置投递状态与 next attempt。

## 4. 共享 DTO 约定

### 4.1 Scope

所有 RPC 使用相同 message：Region 和 Environment 必填，Stage 可空。adapter 调用统一规范化函数，不在 handler 内自行 trim/拼 key。

### 4.2 Revision 与并发控制

- 分发可见元数据与数据携带 ConfigRevision；Release 聚合携带 EntityRevision；Outbox 携带 LeaseRevision。
- 更新命令携带语义明确的 expected_*_revision，禁止裸 expected_revision 横跨不同资源。
- revision 不匹配映射为 gRPC `Aborted`，并在 error detail 中返回 current revision。
- 读取响应里的 generation/revision 是观察信息，不作为权限凭证。

### 4.3 幂等

- 创建发布单必须有 16..128 字符 idempotency key。
- 同一 Principal + key + 相同规范请求返回原结果。
- 同一 Principal + key + 不同规范请求返回 `AlreadyExists`，error code 为 `IDEMPOTENCY_KEY_REUSED`。
- EXECUTE/ADVANCE/APPROVE/REJECT/ROLLBACK 要求 action_request_id；同 ID + 同请求返回原结果，同 ID + 不同请求返回 `IDEMPOTENCY_KEY_REUSED`，不同 ID + stale order revision 返回 Aborted。

### 4.4 列表与分页

- 页码从 1 开始。
- 未指定 page size 使用接口默认值。
- 非法或超过上限返回 `InvalidArgument`。
- 所有列表必须有稳定排序，最后追加稳定 ID。
- 空列表是成功，不返回 NotFound。

## 5. 错误模型

传输层使用标准 gRPC status，并附加稳定的业务 detail：

```text
ErrorDetail {
  code: string
  message: string
  field: string?
  resource_type: string?
  resource_id: string?
  current_revision: int64?
  trace_id: string?
}
```

| gRPC status | 使用场景 |
|---|---|
| `InvalidArgument` | 字段、类型、范围、分页、未知枚举错误 |
| `Unauthenticated` | token 缺失或无效 |
| `PermissionDenied` | Principal 无资源/动作权限 |
| `NotFound` | 单对象不存在 |
| `AlreadyExists` | 唯一键、活动模板或幂等键冲突 |
| `FailedPrecondition` | 对象禁用、步骤前置条件不满足、坏 option source |
| `Aborted` | expected revision 冲突、并发推进失败 |
| `ResourceExhausted` | 查询、响应、Watch 队列容量上限 |
| `Unavailable` | 尚无可用 snapshot 或依赖暂时不可用 |
| `Internal` | 不变量破坏或未分类内部失败 |

领域错误是带 code 的普通 Go error，不 import gRPC status。transport adapter 是唯一错误映射位置。

## 6. 认证上下文

Principal 不出现在业务请求体。Kitex middleware 验证标准 Bearer JWT 或开发静态 token，将以下只读值放入 Go context：Subject、DisplayName、Roles、允许的 Scope。领域模块只接收已构造的 Principal。

不得信任 `X-User-Email`、请求体 creator、前端角色选择器或任意未签名 header。前端的角色切换只允许在 demo 数据 adapter 中工作。

## 7. Contract Freeze 验收

- Buf lint 和 breaking check 可运行。
- 固定 protoc/Kitex 生成工具版本，重新生成后 git diff 为空。
- 每个枚举显式保留 `UNSPECIFIED = 0`，adapter 拒绝它进入领域。
- 错误映射、分页默认值、幂等语义有 transport contract tests。
- grpc-go 和 Kitex client 经 TLS terminator 访问 Kitex server，完成标准 gRPC unary/Watch/status-detail interoperability smoke；生产 Envoy 配置另做 mTLS 负向与拓扑测试。
- OpenAPI DTO 与 BFF handler 校验一致，前端不手写第二套冲突类型。
