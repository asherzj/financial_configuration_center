# MySQL、GORM 与 Migration 详细设计

## 1. 技术边界

V1 只支持 MySQL 8.4.11 LTS，并以 MySQL 8.0.46 作为兼容测试目标。所有表使用 InnoDB、`utf8mb4` 和显式二进制/大小写敏感 collation；标识字段优先 `ascii_bin`。

GORM 只在 `internal/platform/mysql` adapter 中使用：

- 禁止 AutoMigrate。
- 禁止把 GORM model 传入领域或 application 模块。
- 禁止使用 GORM hooks 产生 revision、change log、audit、outbox 等领域副作用。
- 禁止全局 `*gorm.DB` 单例；composition root 构造并注入。
- 预加载只用于有界关系；列表和大集合必须显式查询，避免 N+1。
- revision allocator、`FOR UPDATE`、`SKIP LOCKED` 等使用参数化 SQL，并留在 adapter 内。

## 2. 连接配置

DSN 必须设置 `parseTime=true`、`loc=UTC`、合理的 read/write/connect timeout，并关闭会改变语义的宽松 SQL mode。启动时执行只读 capability/schema gate：只接受 MySQL 8.4.11+ LTS 线或兼容测试用 8.0.46+ 线，拒绝 MariaDB；要求默认引擎及全部 FinConfig 业务表为 InnoDB、session time zone 为 UTC、UTC 偏移为 0、启用 STRICT SQL mode 且禁用 `ALLOW_INVALID_DATES`。所接受版本均原生强制 CHECK constraint。

schema gate 读取 `goose_db_version` 的最新状态，要求构建内 migration manifest 的每个版本都已应用且没有仍处于 applied 状态的未知版本。服务进程只校验，绝不创建 Goose 表、自动迁移或接受运维参数跳过检查；表缺失、版本落后/超前或历史状态不完整均启动失败。

连接池参数显式配置：MaxOpenConns、MaxIdleConns、ConnMaxLifetime、ConnMaxIdleTime。readyz 检查短超时 `PingContext`，但瞬时失败不能直接终止已运行进程。

## 3. Application-owned transaction ports

```go
type CatalogTransactionPort interface { /* Catalog 所需的最小事务能力 */ }
type ReleaseTransactionPort interface { /* Release 所需的最小事务能力 */ }
type AccessTransactionPort interface { /* effective read + audit append */ }
```

每个 application package 声明自己的最小能力接口，禁止建立全系统 god Tx；MySQL adapter 可实现多个 port，但不暴露 `*gorm.DB`。写事务使用 READ COMMITTED 加显式行锁；一致快照与 sensitive reveal 使用 REPEATABLE READ。业务 command 的全部 repository、revision、audit、change log 和 outbox 写入必须使用同一数据库事务。

网络调用、RefreshHint、回调和 trace exporter 不得发生在事务内部。

## 4. Revision 分配

表 `configuration_revision_counters` 固定一行 `counter_name='global'`。在当前 GORM transaction 的同一连接执行：

```sql
UPDATE configuration_revision_counters
SET current_revision = LAST_INSERT_ID(current_revision + 1),
    updated_at = CURRENT_TIMESTAMP(6)
WHERE counter_name = 'global';

SELECT LAST_INSERT_ID();
```

必须断言 UPDATE 影响一行。不得使用 `MAX(revision)+1`、内存原子数或事务外 sequence。分配出的 ConfigRevision 用于本次 command 所有 distribution-visible 对象；多集合 command 可以共享。ReleaseOrder/StepState 的 EntityRevision 与 Outbox 的 LeaseRevision 都是行内局部递增，禁止调用 global allocator。

## 5. V1 逻辑表

Goose migration 创建以下 16 张业务表：

1. `configuration_revision_counters`
2. `configuration_collections`
3. `configuration_records`
4. `configuration_subscriptions`
5. `configuration_models`
6. `release_templates`
7. `release_orders`
8. `release_order_items`
9. `release_step_states`
10. `release_action_requests`
11. `release_operation_logs`
12. `configuration_overlays`
13. `configuration_versions`
14. `configuration_change_log`
15. `outbox_events`
16. `audit_records`

这比原复原提示词多一张通用 `audit_records`，是 V1 的明确决策：ReleaseOperationLog 只描述发布动作，不能替代元数据、读取敏感字段和系统操作的统一审计。

## 6. 关键表约束

### 6.1 Catalog

`configuration_collections`：

- PK `name VARCHAR(191) ascii_bin`
- fields、key_fields 为 JSON，顶层数组 CHECK
- schema_version、revision 大于 0
- status 只允许 ENABLED/DISABLED

`configuration_records`：

- PK `(collection_name, environment, record_key)`
- data 为 JSON object
- FK collection RESTRICT
- index `(collection_name, environment, config_revision)`

`configuration_subscriptions`：

- PK stable id
- UNIQUE `(consumer_id, collection_name, index_name)`
- index_fields JSON array
- cardinality 枚举 CHECK
- consumer/enabled 和 collection/enabled 双向索引

`configuration_models`：

- PK code，FK collection
- fields、query_page、release keys、auto fill、release types 等使用 JSON
- `query_page` 顶层 object CHECK
- enabled、revision、审计列

JSON 内部复杂约束由 ModelCompiler 负责，数据库只守顶层类型和关系完整性。

### 6.2 Release

`release_templates` 使用 PK `(code, version)`，并以 nullable `active_slot='A'` + UNIQUE `(model_code, release_type_code, active_slot)` 保证每个模型/发布类型只有一个活动模板。历史版本 active_slot 为 NULL。

`release_orders`：

- PK id，release_number 全局唯一
- UNIQUE `(created_by, idempotency_key)`
- 保存 template_snapshot JSON，不回查活动模板决定在途流程
- status 与 completed_at CHECK
- current_step_code、order revision、scope 和 model/template FK
- 保存规范请求 SHA-256，用于识别幂等 key 复用不同请求
- `compensates_order_id` 可空自引用 FK，补偿发布指向已成功原单

`release_action_requests` 使用 PK `(release_order_id, action_request_id)`，保存 normalized request digest 和完整结果投影；同 ID 同请求可返回原结果，同 ID 不同请求拒绝。

`release_order_items`：

- UNIQUE `(release_order_id, position)` 和 `(release_order_id, collection_name, record_key)`
- `active_conflict_key CHAR(64)` 可空且全局 UNIQUE
- ADD/MODIFY/DELETE 的 base_before/effective_before/after 组合由 CHECK 约束
- 固化 expected_record_revision 和 expected_collection_revision

active conflict key 使用规范长度前缀编码后求 SHA-256：BASE_FINAL 编码 `BASE + collection + environment + record_key`，OVERLAY_FINAL 编码 `OVERLAY + collection + full scope + record_key`。ReleaseOrder 终态时置 NULL。

`release_step_states` 使用 PK `(release_order_id, step_code)` 和 UNIQUE sequence。`context` 只保存模板参数解析后的稳定键值，不保存可执行脚本；`effect JSON NULL` 保存版本化补偿数据，只供 Release 聚合读取，不进入详情响应或日志。

`release_operation_logs` append-only；禁止 UPDATE/DELETE repository 方法。

### 6.3 Overlay 与版本

`configuration_overlays`：

- UNIQUE `(collection_name, region, environment, stage, record_key)`
- action/content CHECK
- `rollout_ranges` JSON array；空数组表示全 Scope，非空时每项为 0..99 不重叠闭区间
- effective_from/effective_until 与范围 CHECK
- `activated_revision/activated_at`、`expired_revision/expired_at` 成对为空或成对非空
- expired 必须发生在 activated 之后
- release_order_id FK RESTRICT

`configuration_versions` 使用 PK `(collection_name, environment)`，保存 revision、BaseDigest、OverlayDigest、最近 release order 和 updated_at。

`configuration_change_log` 使用 BIGINT AUTO_INCREMENT cursor，按 `(collection_name,id)` 和 `(release_order_id,id)` 建索引；append-only。

### 6.4 Outbox 与 Audit

`outbox_events`：

- stable id + BIGINT AUTO_INCREMENT `sequence_no` UNIQUE
- idempotency_key UNIQUE
- status PENDING/PROCESSING/SENT/DEAD_LETTER
- `lease_revision BIGINT UNSIGNED` 从 1 开始，每次领取、投递结果或人工 replay 条件更新并递增
- attempts、next_attempt_at、locked_by、locked_until
- payload JSON object、payload_version > 0
- relay index `(status,next_attempt_at,sequence_no)`

`audit_records`：

- BIGINT AUTO_INCREMENT id
- occurred_at、principal subject/display name、action、resource type/id
- scope 三列、result、trace id、request id
- before/after JSON 可空且在写入前字段级脱敏
- metadata JSON 只能存非敏感诊断字段
- 按 resource、principal、occurred_at 建组合索引
- append-only，无 Update/Delete adapter

## 7. 事务配方

### 7.1 元数据写

1. 开启 write UoW。
2. `SELECT ... FOR UPDATE` 读取目标并校验对应 ConfigRevision。
3. 校验引用和模型编译。
4. 分配 ConfigRevision。
5. 写元数据。
6. 推进受影响 collection/environment version。
7. 写 METADATA change log、audit、outbox。
8. commit 后才发送 RefreshHint。

### 7.2 发布步骤写

1. 锁 release_order、current step 和全部目标 record/overlay。
2. 重新校验 EntityRevision、状态、角色、ExpectedRecordRevision/ExpectedCollectionRevision 和 active conflict。
3. 仅在步骤产生配置效果时分配一个 ConfigRevision；审批/推进不分配。
4. 应用全部 item；任一失败回滚全部。
5. 更新 version/digest、change log、item/step/order、operation log、audit、outbox。
6. commit 后 relay/hint。

### 7.3 Outbox 领取

relay 在短事务内使用 `SELECT ... FOR UPDATE SKIP LOCKED LIMIT ?` 领取到期 PENDING 或锁过期 PROCESSING 事件，设置 locked_by/until 并递增 LeaseRevision 后提交。外部投递在事务外执行，再按 id+expected LeaseRevision 条件 UPDATE 标记 SENT 或安排重试。相同 idempotency key 的接收方效果必须幂等。

## 8. Migration 策略

- 路径 `db/migrations/mysql/`，文件名使用递增时间戳或六位序号，选定后不混用。
- 每个 migration 包含 Goose Up/Down；不可安全回退的数据 migration 可以让 Down 明确失败并在说明中记录。
- 生产启动不自动执行 migration；独立命令或部署 job 执行。
- `internal/platform/mysql/migrations` 维护构建内 expected version 与 16 张业务表 manifest；测试必须证明 version manifest 与 migration 文件集合完全一致、table manifest 与迁移后实际业务表完全一致，新增 migration/表时清单在同一提交更新。
- seed demo 与生产 migration 分开。生产 migration 除 global counter 不写业务数据。
- migration contract test 从空库执行到最新，也从前一发布升级到最新。
- 领域、审计与调度时间统一使用 `DATETIME(6)`，连接时区固定 UTC，避免 EffectiveUntil 的 2038 范围限制。
- ID/code 使用 ASCII + `ascii_bin`；展示文本使用 `utf8mb4_0900_as_cs`。标识构造器 trim 后若发生变化则拒绝，避免 VARCHAR trailing-space 歧义。
- Record Data JSON 只存 canonical string values，不混入 JSON number/bool。所有 FK delete 显式 RESTRICT；exact CHECK/INDEX 写在 Goose SQL 中，不依赖 GORM tag。

## 9. GORM Adapter 规则

- persistence struct 使用 `dbXxx` 命名并留在 adapter 包内。
- JSON 字段通过显式 marshal/unmarshal 转换，转换失败视为数据损坏，不静默给零值。
- 所有查询显式 `WithContext(ctx)`。
- 禁止 `Save` 的全字段模糊语义；使用明确 `Create` 或带 WHERE revision 的 `Updates`。
- optimistic update 必须检查 RowsAffected。
- 用户值只能作为 bind parameter，标识选择只能来自编译后的固定代码分支。
- SQL 错误统一翻译成 adapter error：duplicate、FK、deadlock、lock timeout、not found；不把驱动字符串泄漏到 transport。

## 10. 持久化验收

- MySQL 8.4.11 和 8.0.46 的 migration 与 repository contract tests 全部通过。
- race test 不发现共享 GORM/session 状态问题。
- 并发创建同 active conflict 只有一个成功。
- revision 分配在回滚后不产生可见 CollectionVersion。
- release 事务注入任一步失败时，记录、版本、日志、审计和 outbox 均无部分提交。
- Overlay boundary 被多个 reconciler 并发领取时只推进一次 revision。
- outbox 多 relay 领取无重复并允许锁超时恢复。
