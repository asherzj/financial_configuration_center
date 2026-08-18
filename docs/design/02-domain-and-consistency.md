# 领域模型与一致性详细设计

## 1. 领域划分

V1 使用一个 Go module 和三个核心领域模块，不拆成独立微服务领域仓库：

- Catalog：CollectionDefinition、ConfigurationRecord、Subscription、ConfigurationModel。
- Distribution：CollectionVersion、ChangeLogEntry、ConfigurationSnapshot、RefreshHint。
- Release：ReleaseTemplate、ReleaseOrder、ReleaseItem、ReleaseStepState。

PageQuery 是消费 Catalog + Distribution 只读模型的深模块，不拥有写模型。Identity、Audit、Outbox 是支撑能力。

## 2. 公共值对象

### 2.1 Scope

```go
type Scope struct {
    Region      string
    Environment string
    Stage       string
}
```

构造函数负责 trim、必填校验和长度限制。比较大小写敏感。规范 key 使用长度前缀编码，不暴露 `region/env/stage` 拼串给调用者。

CollectionVersion 以 `(collection, environment)` 为键。该 environment 下任意 region/stage Overlay 变化都会推进同一版本，这是有意的保守失效：可能多刷新，但不会漏刷新。

### 2.2 AuditStamp 与 Principal

所有可变聚合带 CreatedAt/By、UpdatedAt/By。时间统一 UTC，领域层通过注入 `Clock` 获取，不直接调用 `time.Now()`。Principal Subject 是审计身份，DisplayName 只用于展示。

### 2.3 Digest

算法固定 `SHA-256`，Value 为 64 位小写十六进制。空集合有固定摘要：对稳定编码后的空数组 `[]` 求 SHA-256，不使用空字符串或 NULL 代替。

## 3. 规范编码

### 3.1 标量规范化

| 类型 | 规范字符串 |
|---|---|
| STRING | 原值；不二次 trim 业务值 |
| INT64 | 十进制，无前导 `+` 和无意义前导零 |
| FLOAT64 | `strconv.FormatFloat(v, 'g', -1, 64)`；拒绝 NaN/Inf |
| BOOL | `true` 或 `false` |
| TIMESTAMP | UTC RFC3339Nano |
| JSON | 解析后递归稳定 key 排序、无无意义空白的 UTF-8 JSON |

### 3.2 RecordKey 和索引 key

KeyFields 按定义顺序提取规范字符串，编码为稳定 JSON 字符串数组的 UTF-8 字节，再做 URL-safe base64 无 padding。该算法同时用于 RecordKey 和二级索引 key；禁止逗号、下划线或控制字符拼接。

算法必须用碰撞、Unicode、空字符串和字段缺失测试固定。空字符串是合法值，字段缺失是错误。

### 3.3 摘要输入

- 基础摘要：按 RecordKey 升序排列，每项编码 `[record_key, canonical_object]`。
- Overlay 摘要：筛出目标 environment 的全部 region/stage rule，按 scope key、record key、rule id 排序，包含 action、content、显式激活/失效状态。
- Model/Definition 摘要不混入 BaseDigest；元数据变化仍推进 revision，并通过 METADATA change log 触发快照重建。

## 4. Catalog 模型

### 4.1 CollectionDefinition

字段类型为 STRING/INT64/FLOAT64/BOOL/TIMESTAMP/JSON。Fields 非空、Name 唯一、DisplayOrder 唯一且非负；KeyFields 非空、有序且只能引用字段。任何 Sensitive 字段都不得进入 KeyFields、Subscription IndexFields 或可读目标摘要，避免通过 key/index/日志旁路泄漏。

CollectionDefinition 是数据契约真相。已有记录时：

- 可以增加非必填字段或带有效默认值的必填字段。
- 不得直接删除 KeyField、改变字段类型或改变 KeyFields 顺序。
- 破坏性 schema 变更必须创建新 ConfigurationCollection 并通过发布迁移数据。

### 4.2 ConfigurationRecord

ConfigurationRecord 属于一个明确的 Environment，身份为 `(collection, environment, record_key)`。Data 保存完整字段 map，不携带未知字段。缺失非必填且有默认值的字段在写入前物化默认值；缺失无默认值的可选字段保持不存在，不能改写成空字符串。

ADD 要求目标不存在；MODIFY 要求目标存在且 RecordKey 不变化；修改 KeyField 表示 DELETE 旧 key + ADD 新 key 两个 ReleaseItem。DELETE 只保存 before image，不保存 after。

### 4.3 Subscription

唯一键为 `(consumer_id, collection, index_name)`。IndexFields 非空且存在于定义；ONE_TO_ONE 在构建 snapshot 时发现重复必须使该 collection 构建失败。禁用 Subscription 不再向新快照分发，但不直接修改旧 SDK snapshot；版本变化促使 SDK 删除集合或索引。

### 4.4 ConfigurationModel

Model 绑定一个 Collection。CollectionDefinition 负责数据语义；ModelField 负责 UIControl、Queryable、Editable、AllowedFilterOperators、OptionSource 和显示顺序。

为兼容 transport/persistence，ModelField 可以重复 Type/Required/Sensitive/DefaultValue/ValidationRules，但 `ModelCompiler` 必须要求与 CollectionDefinition 完全相等。编译失败的模型不得启用，也不得进入 snapshot。

## 5. Overlay 与有效记录

OverlayRule 对一个 Scope + RecordKey 执行 ADD/MODIFY/DELETE。ADD/MODIFY content 是完整合法记录；DELETE content 为空。可选 `RolloutRanges []BucketRange` 表示 Client bucket 0..99 的不重叠闭区间；空集合表示 Scope 内全部客户端。

应用顺序固定：

1. 从基础记录 map 开始。
2. 选择 environment 相等、region 相等、已显式激活且未显式失效的规则；真实 SDK 请求还必须命中 Client bucket，普通 QueryPage 不选择带 RolloutRanges 的规则。
3. 先应用 Stage 为空的 environment overlay，再应用 Stage 精确匹配的 overlay。
4. 同一层级同一 RecordKey 只允许一条规则，数据库唯一约束保证。
5. ADD 目标已存在、MODIFY/DELETE 目标不存在均视为不变量错误，使 collection refresh 失败。

`OverlayDigest` 固定为全部规则按 `(collection, environment, region, stage, record_key)` 排序后，对以下 JSON tuple 数组计算 SHA-256：`[collection, region, environment, stage, record_key, action, content, sorted_rollout_ranges, activated, expired]`。`content` 使用规范 JSON object，range 按 start/end 排序；数据库 ID、ReleaseOrderID、时间戳和 revision 不参与摘要。这样摘要只表达可见语义，同一规则因重建、读取顺序或历史归属不同不会产生伪变化。空规则集摘要与规范空 JSON 数组 `[]` 的 SHA-256 相同。

### 5.1 时间边界

仅比较 `time.Now()` 会让结果在没有 revision 时变化，V1 禁止这种行为。Overlay 持久化以下显式状态：

- `activated_revision/activated_at`
- `expired_revision/expired_at`

创建规则时：EffectiveFrom 为空或已到达则同事务激活；未来规则保持未激活。`OverlayBoundaryReconciler` 轮询到期规则，锁定后分配 revision，写 marker、CollectionVersion、ChangeLog、Audit 和 Outbox。失效同理。

EffectiveFrom/Until 是“不早于”语义，实际生效延迟受 reconciler poll interval 限制，默认目标小于 5 秒。Config Server 只读取 marker，不自行按墙钟切换结果。

## 6. Distribution 模型

### 6.1 CollectionVersion

ConfigRevision 使用全局 allocator，严格递增但允许因事务回滚或其他集合写入出现数字间隙。消费者只能比较大小，不假定连续。ReleaseOrder/StepState 的 EntityRevision 与 Outbox 的 LeaseRevision 都不是配置水位，不得写入 CollectionVersion。

任何影响可见配置、订阅、定义或模型的写入都推进绑定 collection/environment 的 ConfigRevision。基础变更只影响 ReleaseOrder 目标 Environment，并重算该 Environment 的 BaseDigest；Overlay 及边界变更只推进目标 Environment 并重算 OverlayDigest。Collection 创建时，为部署配置中的每个受管 Environment 初始化空 version row；运行时不得凭空创建 `default`。

### 6.2 ChangeLogEntry

ChangeLog 是 append-only 应用层日志，After 不是刷新权威事实。增量刷新以 cursor 找出可能变化的 record key，然后在同一只读事务中回查最终态。只有新集合构建和校验全部成功才推进该集合 cursor。

### 6.3 ConfigurationSnapshot

Snapshot 顶层和全部嵌套 map/slice 发布后不可变。构建器必须深拷贝数据库结果，预排序 slice，并且不把 GORM model 指针放入 snapshot。

无依赖集合允许一次刷新中成功集合采用新值、失败集合保留旧值。Model 中 COLLECTION OptionSource 建立 collection dependency graph；同一变更涉及的连通依赖闭包必须 all-or-nothing 构建，任一失败则整组保留旧 definition/model/data/version/cursor。成功候选一次发布新的顶层 snapshot identity，QueryPage 只观察一个完整顶层指针。

## 7. Release 聚合概览

ReleaseOrder 是唯一聚合根；ReleaseItem、StepState 和 OperationLog 不允许被 transport handler 独立修改。完整状态机见 `07-release-workflow.md`。

ReleaseTemplate 以 `FinalEffect` 明确声明 `BASE_FINAL` 或 `OVERLAY_FINAL`。在途冲突通过数据库唯一 active conflict key 和事务内领域校验双重保证：BASE_FINAL 按 `(collection,environment,record_key)` 冲突；OVERLAY_FINAL 按 `(collection,full scope,record_key)` 冲突。ReleaseOrder 终态后清空 active key。

## 8. 模块接口

以下是行为形状，不要求按文件逐字复制，但外部接口不得泄漏 repository 或 GORM：

```go
type CatalogCommands interface {
    CreateCollection(context.Context, Principal, CreateCollection) (CollectionDefinition, error)
    UpdateCollection(context.Context, Principal, UpdateCollection) (CollectionDefinition, error)
    UpsertSubscription(context.Context, Principal, SaveSubscription) (Subscription, error)
    CreateModel(context.Context, Principal, CreateModel) (ConfigurationModel, error)
    UpdateModel(context.Context, Principal, UpdateModel) (ConfigurationModel, error)
}

type SnapshotManager interface {
    Current() *ConfigurationSnapshot
    Refresh(context.Context, RefreshRequest) (RefreshResult, error)
}

type PageQuerier interface {
    Query(context.Context, Principal, PageQuery) (PageResult, error)
}

type ReleaseCommands interface {
    Create(context.Context, Principal, CreateReleaseOrder) (ReleaseOrderDetail, error)
    Act(context.Context, Principal, ActOnReleaseOrder) (ReleaseOrderDetail, error)
    CreateCompensation(context.Context, Principal, CreateCompensation) (ReleaseOrderDetail, error)
}
```

Repository 和 UnitOfWork 是模块 implementation 的内部 seam。transport tests 通过上述模块接口的 fake adapter 替换远程依赖；领域测试直接调用纯构造器和状态转换。

## 9. 不变量失败策略

- 用户输入违反规则：稳定领域错误，映射 InvalidArgument/FailedPrecondition。
- expected revision 冲突：不重试写事务，返回 Aborted。
- MySQL deadlock/lock timeout：只对明确幂等的整个 application command 做有限重试，最多 3 次并带 jitter。
- snapshot 单集合不变量失败：保留旧集合和旧 cursor，记录失败，不影响可独立成功的集合。
- 已发布内存对象被修改应通过 race test/封装阻止；不以运行时深拷贝掩盖错误。
