# FinConfig Domain Language

FinConfig 管理结构化配置从定义、查询、受控发布到进程内消费的完整生命周期。本文只定义领域语言；实现约束见 `docs/design/`。

## Configuration catalog

**ConfigurationCollection**:
共享同一字段契约和复合唯一键规则的一组结构化配置记录。
_Avoid_: 动态表、配置表、业务表

**CollectionDefinition**:
ConfigurationCollection 的字段、唯一键、SDK 分发状态和 schema 版本定义。
_Avoid_: 表结构、动态 schema

**ConfigurationRecord**:
ConfigurationCollection 在一个 Environment 中由稳定 `RecordKey` 标识的一条完整基础配置；不同 Environment 的基础配置彼此隔离。
_Avoid_: 行、数据库记录、KV

**RecordKey**:
由 CollectionDefinition 的有序 KeyFields 对规范值进行无碰撞编码得到的稳定记录标识。
_Avoid_: 主键拼串、联合键字符串

**Subscription**:
一个 Consumer 对 ConfigurationCollection 的读取授权和索引声明。
_Avoid_: 订阅消息、监听器

**Consumer**:
消费配置的逻辑应用身份，由稳定 `ConsumerID` 标识。
_Avoid_: 用户、租户、PSM

**ConfigurationModel**:
绑定一个 ConfigurationCollection，并统一声明管理页面查询、编辑和发布行为的模型。
_Avoid_: 页面配置、操作模型、动态表模型

## Scope and effective configuration

**Scope**:
由 Region、Environment 和可空 Stage 组成的配置可见范围；V1 不包含隐式租户或内部泳道语义。
_Avoid_: 泳道、机房标签、PPE、BOE

**RuntimeMode**:
进程运行时的安全模式，只允许 development、test 或 production；它不参与配置 Scope，也不表示进程当前承载的业务 Environment。
_Avoid_: Environment、部署环境、配置环境

**ManagedEnvironment**:
一个 Config Server 实例唯一承载的业务 `Scope.Environment`。实例只能发布、查询和刷新该 Environment 的完整 snapshot，跨 Environment 请求或 Hint 必须在进入刷新队列前拒绝。
_Avoid_: RuntimeMode、当前环境、默认环境

**OverlayRule**:
在特定 Scope 对基础配置执行 ADD、MODIFY 或 DELETE 的条件化覆盖，不直接改写基础记录。
_Avoid_: 灰度表、临时数据

**EffectiveRecord**:
在一个确定时刻和 Scope 下，将有效 OverlayRule 应用于基础 ConfigurationRecord 后得到的只读结果。
_Avoid_: 当前行、最终数据

**CollectionVersion**:
ConfigurationCollection 在一个 Environment 下的单调 ConfigRevision、基础摘要和覆盖摘要。
_Avoid_: MD5 版本、更新时间版本

**ConfigRevision**:
对分发可见事实的全局单调水位；仅配置、定义、模型、订阅、Overlay 等可见事实变化时分配。
_Avoid_: 实体版本、更新时间

**EntityRevision**:
单个可变聚合内部从 1 递增的并发控制版本，不代表配置分发水位。
_Avoid_: ConfigRevision、全局版本

**LeaseRevision**:
Outbox 事件行用于领取、投递和重放 CAS 的局部版本。
_Avoid_: ConfigRevision、事件序号

**ConfigurationSnapshot**:
一次原子发布的不可变一致视图，包含定义、模型、订阅、版本、基础记录、覆盖规则和游标。
_Avoid_: 缓存 map、内存数据库

**SnapshotIdentity**:
由部署级 ServerEpoch、进程级 ServerInstanceID 和实例内 SnapshotGeneration 组成的快照身份；Generation 不跨实例比较大小。
_Avoid_: 全局快照版本

**ServerEpoch**:
同一 ManagedEnvironment 的部署级恢复世代标识；正常重启保持不变，PITR 或灾备恢复后必须更换，SDK 看到变化时无条件 FULL。
_Avoid_: 数据库 revision、启动时间、实例 ID

**ServerInstanceID**:
Config Server 进程实例的唯一标识；每次进程启动均不同，不能作为部署世代或配置版本比较。
_Avoid_: ServerEpoch、Pod 名默认值、SnapshotGeneration

**SnapshotInstance**:
一次 snapshot lineage 的唯一标识；lineage 内 SnapshotGeneration 从 1 递增，重建 lineage 时更换。
_Avoid_: ServerInstanceID、ConfigRevision、全局版本

**RefreshHint**:
允许丢失、重复和乱序，只用于缩短快照收敛时间的非权威提示。
_Avoid_: 配置事件、权威变更消息

**RefreshCoordinator**:
Config Server 内唯一持有 Hint、Version Poll 和启动刷新目标水位的调度器；按集合合并最大 revision/cursor、串行触发 SnapshotManager，并在失败后有界退避重试。
_Avoid_: Hint 队列、第二个 SnapshotManager、事件消费者

## Identities

**Principal**:
通过浏览器 OIDC 建立的人类操作者身份，包含角色和允许 Scope，用于管理与发布操作。
_Avoid_: Consumer、服务账号、用户 DTO

**ConsumerIdentity**:
通过 Consumer JWT 建立的 SDK 服务身份，以 token subject 为 ConsumerID，并在 handler 中与请求 ConsumerID 和 Scope 绑定。
_Avoid_: Principal、ClientID、内部调用者

**InternalCallerIdentity**:
通过短期 Internal JWT 建立的 BFF、Control Plane relay 或诊断服务身份，包含服务 subject、角色和允许 Scope。
_Avoid_: Principal、Consumer、Envoy 转发证书字段

## Page query

**QueryPageDefinition**:
ConfigurationModel 中声明投影、稳定排序、分页和查询类型的页面查询定义。
_Avoid_: SQL 模板、查询脚本

**PageQuery**:
经过模型字段和操作符白名单校验的声明式页面查询。
_Avoid_: SQL 查询、动态查询字符串

**OptionSource**:
SELECT 字段的受管选项来源，只能是模型内静态选项或另一个 ConfigurationCollection。
_Avoid_: 外部 SQL、任意接口选项

## Release workflow

**ReleaseOrder**:
一组配置变更及其审批、分阶段生效、补偿和审计状态的持久化聚合根。
_Avoid_: 发布任务、工单、Change Gate

**ReleaseItem**:
ReleaseOrder 中针对一个 RecordKey 的一条期望最终效果，分别保存基础前态、有效前态、目标后态和创建时并发水位。
_Avoid_: 批量数据、变更行

**ReleaseTemplate**:
有版本且不可变的有序发布步骤定义；ReleaseOrder 创建时保存其快照。
_Avoid_: 在线流程配置、审批模板

**FinalEffect**:
ReleaseTemplate 声明的最终持久效果，只能是 BASE_FINAL 或 OVERLAY_FINAL，并决定冲突边界、允许步骤和补偿语义。
_Avoid_: 是否包含 BASE_APPLY 的隐式猜测

**ReleaseStep**:
ReleaseTemplate 中定义允许动作、前置条件、持久化效果和补偿语义的步骤。
_Avoid_: 节点任务、阶段脚本

**ChangeDraft**:
尚未提交为 ReleaseOrder 的拟议变更集合；它没有发布编号、审批或工作流状态。
_Avoid_: 草稿发布单、临时数据库记录

**CompensatingRelease**:
针对已成功 ReleaseOrder 创建的新 ReleaseOrder，用反向 ReleaseItem 恢复业务效果。
_Avoid_: 成功单回滚、篡改历史
