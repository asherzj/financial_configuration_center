# 发布工作流详细设计

## 1. 聚合与职责

ReleaseOrder 是聚合根，内部管理 Items、StepStates、ApprovalState 和 OperationLogs。transport handler、GORM repository 和 StepExecutor 都不得绕过聚合直接决定状态转换。

ReleaseOrder 创建后保存完整 ReleaseTemplate snapshot；活动模板更新不影响在途单。

## 2. 状态与动作

Order status：IN_PROGRESS、SUCCEEDED、ROLLED_BACK、REJECTED、FAILED。V1 application 不主动进入 FAILED；该值只为历史与向前兼容保留。

统一动作：EXECUTE、ADVANCE、APPROVE、REJECT、ROLLBACK。

| 当前步骤/状态 | 允许动作 | 成功结果 |
|---|---|---|
| PENDING 的 MANUAL_REVIEW | EXECUTE | approval=PENDING，step=EXECUTING |
| EXECUTING 的 MANUAL_REVIEW | APPROVE | step=APPROVED |
| EXECUTING 的 MANUAL_REVIEW | REJECT | step=REJECTED，order=REJECTED |
| PENDING 的自动步骤 | EXECUTE | step=EXECUTED，持久化步骤效果 |
| EXECUTED/APPROVED，且有下一步 | ADVANCE | CurrentStep 指向下一步 |
| COMPLETE/PENDING | EXECUTE | order=SUCCEEDED，清 conflict key |
| IN_PROGRESS 且已有可补偿效果 | ROLLBACK | 逆序补偿全部效果，order=ROLLED_BACK |

终态拒绝普通动作。SUCCEEDED 的业务恢复只能 CreateCompensatingRelease。

## 3. ReleaseTemplate 校验

- Code/version 稳定，Steps 非空，Sequence 从 0 连续递增，step code 唯一。
- 最后一步必须 COMPLETE，且 COMPLETE 只能出现一次。
- BASE_APPLY 最多一次。
- 同一模板在 BASE_APPLY 前只能选择一种 rollout 策略：单个 OVERLAY_APPLY，或一个/多个 PERCENT_ROLLOUT；两者不得混用。
- MANUAL_REVIEW、COMPARE 可多次。
- 数据变更步骤必须声明 REQUIRED/OPTIONAL rollback policy；FORBIDDEN 步骤之后不得允许普通 rollback。
- RequiredRoles 非空且属于受管角色集合。
- Params 是固定 allow-list，不接受脚本/SQL/模板表达式。
- ObservabilityLink placeholder 只允许 release_id、model、region、environment、stage、step、target。
- FinalEffect 必须为 BASE_FINAL 或 OVERLAY_FINAL。BASE_FINAL 必须且只能有一个 BASE_APPLY；OVERLAY_FINAL 禁止 BASE_APPLY。TemplateValidator 同时校验 Stage、SchedulingAllowed 和最大调度窗口。
- Params 进入领域前编译为 typed value：MANUAL_REVIEW 仅 `selfApprovalPolicy`；PERCENT_ROLLOUT 仅规范 ranges；BASE_APPLY 仅 `cleanupScopeOverlay=true`；COMPARE 仅 `mode`/可选 `previewBucket`；其余步骤无 params。未知 key 拒绝。

## 4. StepExecutor 接缝

```go
type StepExecutor interface {
    Type() ReleaseStepType
    ValidateTemplate(ReleaseStepDefinition) error
    Execute(context.Context, ReleaseTransactionPort, *ReleaseOrder) (StepEffectEnvelope, error)
    Compensate(context.Context, ReleaseTransactionPort, *ReleaseOrder, StepEffectEnvelope) error
}
```

注册表在 composition root 固定注入。V1 只注册通用六种类型，不支持运行时插件或业务专用 handler。Release application 自己声明最小 ReleaseTransactionPort；executor 不得 import GORM repository。StepEffectEnvelope 使用 `effectVersion + effectType` 的闭合 union 保存补偿所需 before state，未知版本拒绝补偿；COMPARE 使用独立 CompareStepResult。私有效果不映射到详情响应。

## 5. 创建发布单

Create command：

1. 校验 idempotency、scope、model、release type、batch 上限 500。
2. 加载 enabled Model 和 active Template。
3. 把 SINGLE/BATCH 规范为非空 items。
4. 按 CollectionDefinition 解析 canonical BaseBefore/EffectiveBefore/After；调用方给出的 AutoFill 字段被忽略，由服务端生成。MODIFY 的 preserve_sensitive_fields 从事务内权威前态复制，掩码字符串永远不是字段值。
5. 计算 RecordKey、Target、TargetDescription。
6. 批内 `(collection,record_key)` 去重。
7. 事务内锁定并批量读取目标 Environment 的基础状态、有效状态和 CollectionVersion：
   - ADD：不存在且 expected revision=0；
   - MODIFY：存在、revision 匹配、RecordKey 不变；
   - DELETE：存在、revision 匹配；
   每个 Item 固化 `BaseBefore`、`EffectiveBefore`、`ExpectedRecordRevision` 和 `ExpectedCollectionRevision`。SELECT 输入还必须在同事务针对当前 OptionSource 校验存在、未 Disabled 且满足 FixedFilters；与 PageQuery 共享 ResolvedOptionSet 规则。
8. 计算 active conflict key：BASE_FINAL 为 collection/environment/record，OVERLAY_FINAL 为 collection/full-scope/record；并依赖唯一索引兜底并发。
9. 保存 Order、Items、全部 StepStates、TemplateSnapshot、创建 audit。

Create 不修改配置，不写 CONFIGURATION_CHANGED outbox。

## 6. 步骤执行语义

每次动作必须携带 UUIDv4 `action_request_id`，并在一个 write transaction port 中重新 `FOR UPDATE` 加载 Order、当前 Step 和 Items。`(order_id, action_request_id)` 唯一保存规范请求摘要与结果：同 ID 同请求返回原结果，同 ID 不同请求返回 `IDEMPOTENCY_KEY_REUSED`；不同 ID 的 expected order revision/current step 不一致返回 Aborted。锁外加载内容不得用于写决策。

### MANUAL_REVIEW

EXECUTE 创建内置 ApprovalState=PENDING。APPROVE/REJECT 必须是拥有步骤 RequiredRole 且不能违反 self-approval policy 的 Principal。V1 默认禁止发布创建人审批自己的生产 Environment；非生产可配置允许。

REJECT 使 order 终态 REJECTED、completed_at 写入、active conflict key 清空；尚未应用的 items 保持 PENDING。

### OVERLAY_APPLY

要求 Scope.Stage 非空。对每个 item 写或替换该 Scope 的 OverlayRule，保存 previous/new rule 作为 OverlayStepEffect。OVERLAY_FINAL 根据目标 Environment base 与 desired effective state 编译规范 ADD/MODIFY/DELETE rule，而不是照抄用户 action。推进 environment version、OverlayDigest、change log、audit 和 outbox。

### PERCENT_ROLLOUT

用于同一 Environment 内按稳定 Client bucket 扩大覆盖：

- 每个 step params 包含一个或多个闭区间 `[start,end]`，0..99。
- 同一模板所有 percentage 区间不得重叠；按步骤执行后形成已执行区间的并集。
- OverlayRule 保存 `rollout_ranges`，空表示所有客户端；percentage rule 必须非空。
- Config Server 用 ConsumerID + ClientID 的协议 hash 选择命中 rule。
- EXECUTE 把本步骤新区间原子加入所有 items 的 OverlayRule，并保存旧 ranges/effect。
- QueryPage 普通查询不应用 percentage rule；发布诊断通过 PreviewBucket 查看。

### BASE_APPLY

仅适用于 BASE_FINAL。对全部 Items 原子执行目标 Environment 的基础 ADD/MODIFY/DELETE，按 item 写 change log。随后删除或失效被本流程替换的同 Scope OverlayRule。BaseStepEffect 对每个 key 保存 previous/applied base 及全部被删除 rules，以便精确补偿。基础变化使用同一 ConfigRevision 只推进目标 collection/environment；每个 collection/environment 只更新一次，并按整批写一个 CONFIGURATION_CHANGED outbox。

BaseBefore 以创建时数据为基线，但执行时必须同时校验 ExpectedRecordRevision 与 ExpectedCollectionRevision；冲突返回 Aborted，不盲写。

### COMPARE

只读计算目标 Scope/可选 preview bucket 下 EffectiveRecord，与 Items 的预期 After 比较，保存期望/实际 digest、差异 record keys 和检查时间。差异正文不放 StepState context。存在差异返回 FailedPrecondition，step 保持 PENDING，并写失败 OperationLog/Audit。

### COMPLETE

要求之前全部步骤 EXECUTED/APPROVED。BASE_FINAL 不得遗留流程临时 overlays；OVERLAY_FINAL 允许保留 order-owned 最终 overlay。把 Order 置 SUCCEEDED、Items 置 APPLIED、写 completed_at、清 active conflict key。COMPLETE 不再次写配置。

## 7. ADVANCE

ADVANCE 只改变 CurrentStep 和 EntityRevision，不执行下一步。当前 step 必须 EXECUTED/APPROVED，下一步从 TemplateSnapshot 顺序计算。网络重试以 action_request_id 返回原结果；另一个请求携带旧 EntityRevision 返回 Aborted。

## 8. ROLLBACK

只允许 Order=IN_PROGRESS。聚合收集所有已执行且有持久化 effect 的 mutable steps，按逆序在一个事务内调用 Compensate：

- BASE ADD → DELETE；DELETE → 恢复 BaseBefore；MODIFY → 恢复 BaseBefore，同时精确恢复 BASE_APPLY 删除的 Overlay rules。
- Overlay → 恢复执行前 rule 或删除新 rule。
- 每个补偿写 change log；每 collection/environment version/outbox 每批一次。
- 全部成功后 Order/已应用 Items/步骤置 ROLLED_BACK，清 conflict key。

任一补偿失败整批回滚，Order 保持 IN_PROGRESS，并写独立失败审计。模板包含不可补偿 FORBIDDEN 效果时，进入该步骤前就不再暴露 ROLLBACK 动作。

## 9. CompensatingRelease

SUCCEEDED order 不修改历史。CreateCompensatingRelease 根据原 Items 生成反向 Items：ADD→DELETE、DELETE→ADD、MODIFY→交换 before/after，重新读取当前 revision、重新审批并使用当前可用的专用 compensation template。原 order id 写入 `compensates_order_id`。

如果当前配置已经偏离原发布结果，创建失败并要求人工重新生成变更，不自动覆盖第三方更新。

## 10. 失败与 OperationLog

成功日志与主事务一起 append。主事务回滚后的失败日志通过独立短事务写入，内容只包含 action、step、actor、稳定 error code、脱敏 detail、trace id；失败审计写入再次失败时记录 slog/metric 并进入专用内存告警计数，不能伪装已持久化。

参数、锁、依赖或网络失败不把 Order 终态化，保持 IN_PROGRESS/PENDING 并记录失败 operation。不变量破坏返回 Internal、告警并停止继续动作；FAILED/recovery command 留待 V1 后续版本。

## 11. Outbox

配置效果提交产生 `CONFIGURATION_CHANGED`；内置人工审批不要求外部事件。未来 webhook 审批可产生 `APPROVAL_REQUESTED`，但 V1 不实现厂商 adapter。

event payload 包含 version、collections、scope、target cursor、release id、trace id 和 schema version，不包含 before/after 正文。

## 12. 权限

- RELEASE_CREATOR：创建发布单。
- RELEASE_APPROVER：审批，仍受步骤 RequiredRoles 和 self-approval 约束。
- RELEASE_OPERATOR：EXECUTE/ADVANCE/ROLLBACK。
- RELEASE_VIEWER：读取详情。
- SENSITIVE_VIEWER：通过受审计 SensitiveAccessService 查看单个获授权敏感字段；发布详情仍默认掩码。

权限先校验 model/scope，再校验动作；前端按钮隐藏不是授权措施。

## 13. 测试表面

- 模板结构、非法百分比区间、不可补偿链。
- SINGLE/BATCH、自动填充、RecordKey、批内/在途冲突、幂等。
- 所有状态和动作组合、陈旧 revision、并发推进。
- 每种 StepExecutor 的 effect 和 compensation。
- BASE_APPLY 清 overlay、COMPARE mismatch、COMPLETE 前置条件。
- IN_PROGRESS rollback 和 SUCCEEDED compensation。
- 主事务失败无部分写，失败审计可观察。
- outbox 重试不重复配置效果。
