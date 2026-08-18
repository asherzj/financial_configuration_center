# 安全、可观测性与运行详细设计

## 1. 安全模型

V1 使用 RBAC + Scope 授权。Principal 由认证 adapter 构造，包含 Subject、DisplayName、Roles 和 AllowedScopes。每个 application command 先校验资源读取范围，再校验动作角色。

标准角色：

- CONFIG_VIEWER：读取普通配置和模型。
- CONFIG_ADMIN：管理 Collection/Subscription/Model/Template。
- RELEASE_CREATOR：创建 ReleaseOrder。
- RELEASE_APPROVER：审批允许步骤。
- RELEASE_OPERATOR：执行、推进、回滚。
- RELEASE_VIEWER：读取发布详情。
- SENSITIVE_VIEWER：在被授权 Scope 读取敏感字段。
- AUDITOR：读取 AuditRecords。
- PLATFORM_OPERATOR：运行诊断和受控 outbox 重放。

角色名是部署配置中的稳定 code，不映射企业通讯录概念。生产默认禁止发布创建人审批自己的生产发布。

## 2. 认证与传输

- 外部浏览器：BFF 是 confidential OIDC client，使用 Authorization Code + PKCE，强制校验 state、nonce、issuer、audience、exp。
- Session：30 分钟 stateless AEAD sealed HttpOnly cookie，只含 principal/roles/scopes/authTime/sessionID/expiry；secret mount key ring 通过 kid 轮换。不保存 refresh token，过期重新登录；V1 logout 只清 cookie，不提供即时全局撤销。
- CSRF：double-submit token 必须与 sessionID 做 HMAC 绑定，并同时校验 Origin。
- BFF/Control Plane relay → 目标 Envoy：mTLS 验证服务身份，同时每请求签发 60 秒 Ed25519 internal JWT（iss/aud/sub/roles/scopes/jti）；两者都必须通过。Envoy 以同 Pod UDS h2c 转发到 Kitex。
- Go SDK → Config Server Envoy：Consumer JWT + TLS；验证配置化 issuer/audience/JWKS，token `sub` 必须等于请求 ConsumerID。
- 开发静态 token adapter 默认关闭，开启时明确日志警告。
- TLS 最低版本 1.2，推荐 1.3；证书/私钥只从文件或 secret mount 读取。
- Envoy downstream 强制 ALPN `h2`、客户端证书和 SAN allow-list；Kitex backend socket 不通过 Service、hostPort 或跨 Pod TCP 暴露。应用不单独信任 XFCC，仍以 JWT 完成授权。

不得把 token、cookie、证书内容、DSN 密码写入 slog、trace 或错误响应。

## 3. 敏感字段

CollectionDefinition.Sensitive 是数据级标记：

- 未授权响应不投影该字段。
- 查询、排序和 OptionSource 默认不可引用。
- Diff/Audit 只显示掩码和 changed marker。
- ReleaseOrder before/after 数据在数据库中仍需用于补偿，生产应启用 MySQL 磁盘/备份加密和最小数据库权限。
- V1 只通过 SensitiveAccessService 单字段 reveal 明文；读取与 AuditRecord 在同一 Control Plane 事务中完成，审计失败不返回值。BFF 响应 no-store，前端 60 秒后清除内存值。
- reveal 在 REPEATABLE READ 中加载 base、active overlay、model 并复用 Distribution evaluator；必须匹配 record/collection/model revision 与 server epoch/snapshot identity，否则 Aborted。
- SDK Subscription 是对整个 Collection（包括敏感字段）的服务身份读取授权；单字段 reveal 约束只针对 Admin 用户界面，二者不得混为绕过关系。
- ScopePattern 只允许完整 segment 精确值或整个 segment `*`，禁止部分 glob、regex 和业务 DTO 自报 wildcard。production Environment 集合来自显式 allow-list，self-approval 不靠字符串猜测。

## 4. 输入与输出安全

- 所有 DTO 未知字段和未知枚举拒绝。
- 标识长度、集合大小、JSON 深度和总字节有硬上限。
- Regex ValidationRule 在创建模型时编译并限制长度；使用 Go RE2 语义避免回溯攻击。
- ObservabilityLink 只替换 allow-list placeholder，最终 URL scheme/host 受 allow-list。
- 前端 React 默认转义文本；不使用 `dangerouslySetInnerHTML` 展示配置。
- BFF 设置 CSP、HSTS、X-Content-Type-Options、Referrer-Policy 和 frame-ancestors。

## 5. slog 规范

所有进程使用 JSON slog handler。公共属性：

```text
service, version, environment, instance_id,
request_id, trace_id, principal_subject,
rpc_method/http_route, result, error_code, duration_ms
```

模块可增加 collection、model_code、release_id、step_type、revision、generation、cursor、row_count，但禁止：

- ConfigurationRecord Data、before/after、查询条件值；
- token/cookie/DSN；
- Sensitive option label；
- 高基数 payload 或完整 error stack 作为普通字段。

panic 才记录 stack；预期参数/权限错误使用 INFO/WARN，不制造 ERROR 洪水。

## 6. OpenTelemetry Trace

使用标准 W3C trace context，经 HTTP/Kitex/outbox payload 传播。主要 span：

- `catalog.command`
- `release.create`、`release.act`、`release.compensate`
- `mysql.transaction`、`mysql.query`（由 instrumentation 产生且 SQL 参数脱敏）
- `snapshot.refresh` 和每集合 child span
- `config.get_snapshot`、`config.watch`
- `pagequery.query` 及 compile/filter/sort/options 阶段
- `sdk.refresh`、`sdk.build_snapshot`
- `outbox.deliver`

业务 error code 作为低基数 attribute。record key、client id、subject 默认不作为 trace attribute；必要时只放不可逆 hash 并有配置开关。

采样由部署配置决定，默认 parent-based ratio；错误 span 可由 collector tail sampling 提升，应用不自行动态改变采样。

## 7. Prometheus Metrics

必须提供的核心 metric：

```text
finconfig_rpc_requests_total{service,method,code}
finconfig_rpc_duration_seconds{service,method}
finconfig_mysql_tx_total{module,result}
finconfig_mysql_tx_duration_seconds{module}
finconfig_snapshot_refresh_total{mode,trigger,result}
finconfig_snapshot_refresh_duration_seconds{mode}
finconfig_snapshot_generation
finconfig_snapshot_collection_failures
finconfig_watch_connections{service}
finconfig_watch_dropped_events_total{reason}
finconfig_outbox_events{status}
finconfig_outbox_delivery_total{event_type,result}
finconfig_release_actions_total{action,step_type,result}
finconfig_pagequery_total{query_type,result}
finconfig_pagequery_duration_seconds{query_type}
finconfig_sdk_refresh_total{region,result}
finconfig_sdk_callback_total{result}
```

禁止把 collection、model、release id、principal、trace id、record key 用作 Prometheus label。需要资源级诊断时用结构化日志/trace。

## 8. 配置加载

每个进程使用显式 Config struct：默认值 → 可选 YAML → 环境变量覆盖 → Validate。环境变量前缀 `FINCONFIG_`。启动日志打印脱敏后的有效配置摘要。

配置至少覆盖：监听地址、MySQL DSN/池、TLS、auth、timeouts、poll/backoff、容量限制、OTel endpoint、Prometheus、graceful shutdown。未知 YAML 字段拒绝。

运行时不热更新核心安全/数据库配置；变更通过重启部署。ReleaseTemplate/Model 是业务元数据，不混入进程 YAML。

## 9. Health、Readiness 与优雅关闭

每个 Go 进程暴露独立运维 HTTP：

- `/healthz`：进程运行。
- `/readyz`：关键启动条件满足。
- `/metrics`：Prometheus。

Control Plane ready：DB 可用、migration version 符合、RPC running。Config Server 还要求已有 snapshot。BFF 要求至少一个下游 client 可用和静态资源可读。

关闭流程：ready=false → 停止接新请求/流 → 取消后台循环 → 等待事务/handler → flush telemetry（有界）→ 关闭连接。不得 `os.Exit` 跳过 defer；仅 composition root 处理 fatal exit。

## 10. Outbox 运行

- 指数退避初始 1 秒、最大 5 分钟、jitter 20%。
- 默认最大 20 次后 DEAD_LETTER。
- lock lease 默认 30 秒，投递 timeout 10 秒；relay 实例有稳定 ID。
- 人工重放创建新的 audit，重置 next_attempt/status 但不改变原 payload/idempotency key。
- dead-letter 数量和最老 age 告警。

## 11. Overlay boundary reconciler

- 默认每秒扫描未来/已到期边界，批量 100，使用 SKIP LOCKED。
- 多实例并发安全，marker 条件更新保证一次效果。
- lag 指标按最老未处理边界计算。
- 时钟来自 UTC Clock；测试注入 fake clock。
- lag 超阈值 ready 可降级但仍服务旧一致视图，不能让 Config Server自行按墙钟越过 marker。

## 12. 部署

V1 提供 Dockerfile 和 docker-compose：MySQL、各 Kitex 服务及其 Envoy TLS sidecar、admin-bff/admin-console、可选 OTel Collector + Prometheus。所有镜像使用非 root 用户、多阶段构建和固定 base image digest（发布流水线更新）。

生产推荐 Kubernetes Deployment：Control Plane/Config Server/BFF 可多副本；MySQL 是外部托管单写集群。V1 不设计 MySQL HA，但应用必须正确处理连接重建和主从切换后的短暂错误。

Migration 由独立 job 在应用滚动发布前执行。应用启动只校验 schema version，不自动改表。

## 13. 备份与恢复

- MySQL 使用平台级加密备份和 point-in-time recovery。
- 恢复演练必须验证 records、release history、audit、revision counter 和 outbox 一致。
- 从备份恢复后必须更换部署级 `server_epoch`，Config Server/SDK 进行 FULL refresh。`server_instance_id` 每进程唯一，`snapshot_generation` 只在实例内单调，不跨实例或恢复比较大小。

## 14. 安全与运行验收

- dependency/license/vulnerability scan 无高危未处置问题。
- secret scan 不发现凭证。
- auth/CSRF/CORS/CSP/TLS tests。
- 日志/trace/metrics 无配置正文和敏感值。
- SIGTERM 下无新写入、无 goroutine 泄漏、telemetry 有界 flush。
- MySQL 短暂中断后 Control Plane 恢复，Config Server/SDK 始终保留 last-known-good。
