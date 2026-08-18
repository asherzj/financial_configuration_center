# 规范类型参考

本文集中冻结跨模块领域结构。Go 实现可以按包拆分并使用更严格的私有字段/构造器，但不得丢失字段语义、改变枚举集合或把 transport/persistence tag 加到领域类型。

## 1. 枚举

```text
FieldType: STRING, INT64, FLOAT64, BOOL, TIMESTAMP, JSON
UIControlType: INPUT, SELECT, TIME, NUMBER, BOOLEAN, TEXTAREA, JSON
FilterOperator: EXACT, CONTAINS, CLOSED_RANGE, OPEN_RANGE, IN, NOT_IN
SortDirection: ASC, DESC
OptionSourceKind: STATIC, COLLECTION
QueryPageType: ALL, ONLY_DATA
ValidationRuleKind: REQUIRED, ENUM, REGEX, MIN, MAX, MIN_LENGTH, MAX_LENGTH
AutoFillSource: ACTOR_SUBJECT, ACTOR_NAME, CURRENT_TIME, CONSTANT, UUID
DefinitionStatus: ENABLED, DISABLED
SubscriptionCardinality: ONE_TO_ONE, ONE_TO_MANY
ChangeAction: ADD, MODIFY, DELETE
ChangeKind: BASE_RECORD, OVERLAY, METADATA
RefreshMode: FULL, COLLECTIONS, OVERLAYS, INCREMENTAL, VERSION_POLL
RefreshTrigger: STARTUP, OUTBOX, WATCH, POLL, ADMIN, RECOVERY
UpdateEventType: DATA_CHANGED, SUBSCRIPTION_CHANGED, RESYNC_REQUIRED
ReleaseStepType: MANUAL_REVIEW, OVERLAY_APPLY, PERCENT_ROLLOUT, BASE_APPLY, COMPARE, COMPLETE
FinalEffect: BASE_FINAL, OVERLAY_FINAL
RollbackPolicy: REQUIRED, OPTIONAL, FORBIDDEN
ReleaseStatus: IN_PROGRESS, SUCCEEDED, ROLLED_BACK, REJECTED, FAILED
BatchType: SINGLE, BATCH
ReleaseItemStatus: PENDING, APPLIED, ROLLED_BACK, FAILED
ApprovalStatus: NOT_REQUESTED, PENDING, APPROVED, REJECTED
StepStatus: PENDING, EXECUTING, EXECUTED, APPROVED, REJECTED, ROLLED_BACK, FAILED
ReleaseAction: EXECUTE, ADVANCE, ROLLBACK, APPROVE, REJECT
OperationResult: SUCCEEDED, FAILED
OutboxStatus: PENDING, PROCESSING, SENT, DEAD_LETTER
ClientLifecycleState: NEW, STARTING, RUNNING, CLOSING, CLOSED, FAILED
AuditResult: SUCCEEDED, FAILED
```

Proto 为每个枚举增加 `{TYPE}_UNSPECIFIED = 0`，adapter 拒绝 UNSPECIFIED 进入领域。领域枚举不需要合法的零值。

## 2. 公共值对象

```go
type Scope struct {
    Region      string
    Environment string
    Stage       string
}

type AuditStamp struct {
    CreatedAt time.Time
    CreatedBy string
    UpdatedAt time.Time
    UpdatedBy string
}

type Digest struct {
    Algorithm string // SHA-256
    Value     string // 64-char lowercase hex
}

type Principal struct {
    Subject       string
    DisplayName   string
    Roles         []string
    AllowedScopes []ScopePattern
}

type ScopePattern struct {
    Region      string // exact or whole-segment "*"
    Environment string
    Stage       string
}

type ConfigRevision int64
type EntityRevision int64
type LeaseRevision int64
```

ScopePattern 只由 identity adapter 从受信策略构造；普通业务 DTO 不能提交 wildcard。Wildcard 只能占据完整 segment，禁止部分 glob 和 regex。

## 3. Catalog

```go
type ValidationRule struct {
    Kind    ValidationRuleKind
    Params  map[string]string
    Message string
}

type FieldDefinition struct {
    Name            string
    DisplayName     string
    Type            FieldType
    Required        bool
    Sensitive       bool
    DefaultValue    *string
    Description     string
    DisplayOrder    int32
    ValidationRules []ValidationRule
}

type CollectionDefinition struct {
    Name          string
    Description   string
    Fields        []FieldDefinition
    KeyFields     []string
    SDKDeliveryEnabled bool
    SchemaVersion int64
    Status        DefinitionStatus
    ConfigRevision ConfigRevision
    Audit         AuditStamp
}

type ConfigurationRecord struct {
    Collection string
    Environment string
    RecordKey  string
    Data       map[string]string
    ConfigRevision ConfigRevision
    Audit      AuditStamp
}

type Subscription struct {
    ID          string
    ConsumerID  string
    Collection  string
    IndexName   string
    IndexFields []string
    Cardinality SubscriptionCardinality
    Enabled     bool
    ConfigRevision ConfigRevision
    Audit       AuditStamp
}
```

## 4. Overlay、版本与变更

```go
type BucketRange struct {
    Start int32 // 0..99 inclusive
    End   int32 // Start..99 inclusive
}

type OverlayRule struct {
    ID                string
    Collection        string
    Scope             Scope
    RecordKey         string
    Action            ChangeAction
    Content           map[string]string
    RolloutRanges     []BucketRange
    ConfigRevision    ConfigRevision
    ReleaseOrderID    string
    EffectiveFrom     *time.Time
    EffectiveUntil    *time.Time
    ActivatedRevision *ConfigRevision
    ActivatedAt       *time.Time
    ExpiredRevision   *ConfigRevision
    ExpiredAt         *time.Time
    Audit             AuditStamp
}

type CollectionVersion struct {
    Collection     string
    Environment    string
    ConfigRevision ConfigRevision
    BaseDigest     Digest
    OverlayDigest  Digest
    ReleaseOrderID string
    UpdatedAt      time.Time
}

type ChangeLogEntry struct {
    ID             int64
    Collection     string
    Kind           ChangeKind
    Scope          Scope
    RecordKey      string
    Action         ChangeAction
    Before         map[string]string
    After          map[string]string
    ConfigRevision ConfigRevision
    ReleaseOrderID string
    CreatedAt      time.Time
}
```

RolloutRanges 和所有 slice 构造后去重、稳定排序。DELETE 的 Content/After 为空；ADD/MODIFY 是完整记录。

## 5. ConfigurationModel 与 QueryPage

```go
type SelectOptionDefinition struct {
    Code     string
    Label    string
    Disabled bool
}

type SortTerm struct {
    Field     string
    Direction SortDirection
}

type ScalarValue struct {
    Type      FieldType
    Canonical string
}

type FilterCondition struct {
    Field    string
    Operator FilterOperator
    Value    *ScalarValue
    Lower    *ScalarValue
    Upper    *ScalarValue
    Set      []ScalarValue
}

type OptionSourceDefinition struct {
    Kind          OptionSourceKind
    StaticOptions []SelectOptionDefinition
    Collection    string
    ValueField    string
    LabelField    string
    FixedFilters  []FilterCondition
    Sort          []SortTerm
    Limit         int32
}

type QueryPageDefinition struct {
    Enabled           bool
    ProjectionFields  []string
    DefaultSort       []SortTerm
    DefaultPageSize   int32
    MaxPageSize       int32
    AllowedQueryTypes []QueryPageType
}

type ModelField struct {
    Name                   string
    DisplayName            string
    Type                   FieldType
    Required               bool
    Editable               bool
    Queryable              bool
    Sensitive              bool
    UIControl              UIControlType
    AllowedFilterOperators []FilterOperator
    DefaultValue           *string
    DisplayOrder           int32
    OptionSource           *OptionSourceDefinition
    ValidationRules        []ValidationRule
}

type AutoFillRule struct {
    Field  string
    Source AutoFillSource // ACTOR_SUBJECT, ACTOR_NAME, CURRENT_TIME, CONSTANT, UUID
    Value  string
}

type ReleaseTypeDefinition struct {
    Code         string
    Name         string
    TemplateCode string
    Enabled      bool
    Available    bool
    UnavailableReasonCode string
}

type ConfigurationModel struct {
    Code                     string
    Name                     string
    Collection               string
    Fields                   []ModelField
    QueryPage                 QueryPageDefinition
    ReleaseKeyFields         []string
    ReleaseDescriptionFields []string
    AutoFillRules            []AutoFillRule
    AllowedActions           []ChangeAction
    ReleaseTypes             []ReleaseTypeDefinition
    ValidationRules          []ValidationRule
    Enabled                  bool
    ConfigRevision           ConfigRevision
    Audit                    AuditStamp
}

type PageSpec struct {
    Number *int32 // nil => 1; explicit <= 0 is invalid
    Size   *int32 // nil => model default; explicit <= 0 is invalid
}

type PageQuery struct {
    ModelCode    string
    Scope        Scope
    QueryType    QueryPageType
    Conditions   []FilterCondition
    Page         PageSpec
    PreviewBucket *int32
}

type PageInteractionField struct {
    Name          string
    DisplayName   string
    Type          FieldType
    UIControl     UIControlType
    Queryable     bool
    Editable      bool
    Required      bool
    Sensitive     bool
    AllowedFilterOperators []FilterOperator
    DefaultFilterOperator  FilterOperator
    Projected      bool
    KeyField       bool
    AutoFill       *AutoFillRule
    Description    string
    ValidationRules []ValidationRule
    DefaultValue  *string
    DisplayOrder  int32
    Options       []SelectOptionDefinition
}

type PageRow struct {
    RecordKey      string
    RecordRevision ConfigRevision
    Values         map[string]string
    MaskedFields   []string
}

type PageResult struct {
    ModelCode         string
    ModelName         string
    QueryType         QueryPageType
    Rows              []PageRow
    ProjectionFields  []string
    InteractionInfo   []PageInteractionField
    ReleaseTypes      []ReleaseTypeDefinition
    PageNumber        int32
    PageSize          int32
    TotalNumber       int64
    TotalPages        int64
    ServerEpoch         string
    ServerInstanceID    string
    SnapshotGeneration  uint64
    SnapshotInstance    string
    ModelRevision       ConfigRevision
    CollectionRevision  ConfigRevision
}
```

AutoFillSource 必须实现为闭合领域枚举，不能使用任意字符串。其值集合固定为注释中五项。

## 6. Refresh 与服务端 Snapshot

```go
type CollectionRefreshTarget struct {
    Collection   string
    Kinds        []ChangeKind
    MinRevision  int64
    TargetCursor int64
}

type RefreshHint struct {
    EventID        string
    Targets        []CollectionRefreshTarget
    Scope          Scope
    ReleaseOrderID string
    TraceID        string
    OccurredAt     time.Time
}

type RefreshRequest struct {
    Mode           RefreshMode
    Targets        []CollectionRefreshTarget
    Trigger        RefreshTrigger
    ReleaseOrderID string
    TraceID        string
    RequestedAt    time.Time
}

type CollectionRefreshResult struct {
    Collection       string
    PreviousCursor   int64
    CurrentCursor    int64
    PreviousRevision int64
    CurrentRevision  int64
    Changed          bool
    RowsRead         int64
    ChangesApplied   int64
    FallbackUsed     bool
    ErrorCode        string
    ErrorMessage     string
    StartedAt        time.Time
    CompletedAt      time.Time
}

type RefreshResult struct {
    Mode             RefreshMode
    GenerationBefore uint64
    GenerationAfter  uint64
    Succeeded        []CollectionRefreshResult
    Failed           []CollectionRefreshResult
    SnapshotPublished bool
    ReleaseOrderID    string
    TraceID            string
    CompletedAt        time.Time
}

type CompressedBasePayload struct {
    Collection    string
    Codec         string // GZIP
    FormatVersion int32
    BaseData      []byte
}

type CollectionDeliveryPayload struct {
    Base          CompressedBasePayload
    Definition    CollectionDefinition
    Subscriptions []Subscription
    Overlays      []OverlayRule
    Version       CollectionVersion
    EffectiveDigest Digest
}

type ConfigurationSnapshot struct {
    ServerEpoch              string
    ServerInstanceID         string
    SnapshotInstance         string
    Generation               uint64
    PublishedAt              time.Time
    SubscriptionsByConsumer  map[string]map[string][]Subscription
    ConsumersByCollection    map[string][]string
    VersionsByCollection     map[string]map[string]CollectionVersion
    Definitions              map[string]CollectionDefinition
    ModelsByCode             map[string]ConfigurationModel
    RecordsByCollection      map[string]map[string]map[string]ConfigurationRecord // collection/environment/key
    OverlaysByCollection     map[string][]OverlayRule
    CompressedBaseByCollection map[string]CompressedBasePayload
    ChangeCursorByCollection map[string]int64
}
```

## 7. Client SDK

```go
type BackoffPolicy struct {
    Initial    time.Duration
    Maximum    time.Duration
    Multiplier float64
    Jitter     float64
}

type RegionOptions struct {
    Name             string
    Endpoint         string
    Scope            Scope
    Required         bool
    WatchEnabled     bool
    PollInterval     time.Duration
    RequestTimeout   time.Duration
    ReconnectBackoff BackoffPolicy
    TLSProfile       string
}

type ClientOptions struct {
    ConsumerID        string
    ClientID          string
    DefaultRegion     string
    Regions           map[string]RegionOptions
    CallbackWorkers   int
    CallbackQueueSize int
    StartupTimeout    time.Duration
    CloseTimeout      time.Duration
}

type Query struct {
    Region      string
    Collection  string
    RecordKey   string
    IndexName   string
    IndexFields []string
    IndexValues []string
    Limit       int
}

type VersionView struct {
    Collection    string
    ConfigRevision ConfigRevision
    BaseDigest    Digest
    OverlayDigest Digest
}

type UpdateEvent struct {
    ID                   string
    Type                 UpdateEventType
    Region               string
    Scope                Scope
    Collections          []string
    VersionsByCollection map[string]CollectionVersion
    ReleaseOrderID       string
    OccurredAt           time.Time
}
```

Compact storage 的私有实现字段见 `05-go-client-sdk.md`；不作为跨模块 DTO。

## 8. Release

```go
type ObservabilityLink struct {
    Name        string
    URLTemplate string
}

type ReleaseStepDefinition struct {
    Code           string
    Name           string
    Type           ReleaseStepType
    Sequence       int32
    RequiredRoles  []string
    Params         map[string]string
    RollbackPolicy RollbackPolicy
    TimeoutSeconds int32
}

type ReleaseTemplate struct {
    Code               string
    Name               string
    ModelCode          string
    ReleaseTypeCode    string
    FinalEffect        FinalEffect
    SchedulingAllowed  bool
    MaxScheduleWindow  time.Duration
    Version            int64
    Steps              []ReleaseStepDefinition
    AllowedRoles       []string
    ObservabilityLinks []ObservabilityLink
    Enabled            bool
    Audit              AuditStamp
}

type ReleaseOrder struct {
    ID                 string
    ReleaseNumber      string
    IdempotencyKey     string
    RequestDigest      Digest
    ModelCode          string
    ReleaseTypeCode    string
    Scope              Scope
    Status             ReleaseStatus
    CurrentStepCode    string
    TemplateSnapshot   ReleaseTemplate
    Description        string
    AuthorizedRoles    []string
    BatchType          BatchType
    CompensatesOrderID string
    EntityRevision     EntityRevision
    CreatedAt          time.Time
    CreatedBy          string
    UpdatedAt          time.Time
    UpdatedBy          string
    CompletedAt        *time.Time
}

type ReleaseItem struct {
    ID                string
    ReleaseOrderID    string
    Position          int32
    Action            ChangeAction
    Collection        string
    RecordKey         string
    Target            string
    TargetDescription string
    BaseBefore        map[string]string
    EffectiveBefore   map[string]string
    After             map[string]string
    ExpectedRecordRevision ConfigRevision
    ExpectedCollectionRevision ConfigRevision
    Status            ReleaseItemStatus
    ActiveConflictKey *string
    EntityRevision    EntityRevision
    Audit             AuditStamp
}

type ApprovalState struct {
    Provider        string // MANUAL in V1
    ExternalID      string
    Status          ApprovalStatus
    RequestedAt     *time.Time
    RequestedBy     string
    DecidedAt       *time.Time
    DecidedBy       string
    DecisionComment string
}

type StepEffectEnvelope struct {
    EffectVersion int32
    EffectType    ReleaseStepType
    Overlay       *OverlayStepEffect
    Base          *BaseStepEffect
    Percent       *PercentStepEffect
}

type OverlayRuleChange struct {
    RecordKey   string
    PreviousRule *OverlayRule
    NewRule      *OverlayRule
}

type OverlayStepEffect struct { Changes []OverlayRuleChange }
type BaseRecordChange struct {
    RecordKey string
    PreviousBase *ConfigurationRecord
    AppliedBase  *ConfigurationRecord
    RemovedOverlayRules []OverlayRule
}
type BaseStepEffect struct { Changes []BaseRecordChange }
type PercentStepEffect struct { Changes []OverlayRuleChange }

type CompareStepResult struct {
    ExpectedDigest Digest
    ActualDigest   Digest
    DiffKeys       []string
    CheckedAt      time.Time
}

type ReleaseStepState struct {
    ReleaseOrderID string
    StepCode       string
    StepType       ReleaseStepType
    Sequence       int32
    Status         StepStatus
    Context        map[string]string
    Approval       *ApprovalState
    Effect         *StepEffectEnvelope
    CompareResult  *CompareStepResult
    ExecuteCount   int32
    ExecutedAt     *time.Time
    ExecutedBy     string
    RolledBackAt   *time.Time
    RolledBackBy   string
    ErrorCode      string
    ErrorMessage   string
    EntityRevision EntityRevision
    Audit          AuditStamp
}

type ReleaseOperationLog struct {
    ID             string
    ReleaseOrderID string
    ReleaseItemID  string
    StepCode       string
    Action         ReleaseAction
    Result         OperationResult
    ActorSubject   string
    ActorName      string
    Message        string
    ErrorCode      string
    ErrorDetail    string
    TraceID        string
    CreatedAt      time.Time
}

type ReleaseOrderDetail struct {
    Order              ReleaseOrder
    Items              []ReleaseItem
    Steps              []ReleaseStepState
    OperationLogs      []ReleaseOperationLog
    ObservabilityLinks []ObservabilityLink
    Versions           map[string]CollectionVersion
    AllowedActions     []ReleaseAction
}
```

StepEffectEnvelope 是聚合补偿私有数据，不映射到 RPC/HTTP ReleaseOrderDetail。持久化 JSON 必须同时包含 `effectVersion` 与 `effectType`，未知版本拒绝补偿。它可能包含敏感前态，只能存入 release_step_states.effect JSON 并依赖数据库加密保护；不得写日志、审计正文或前端响应。CompareStepResult 不属于补偿 effect。

## 9. Release commands

```go
type CreateReleaseItemInput struct {
    Action                  ChangeAction
    BaseBefore              map[string]string
    EffectiveBefore         map[string]string
    After                   map[string]string
    ExpectedRecordRevision  ConfigRevision
    ExpectedCollectionRevision ConfigRevision
    PreserveSensitiveFields []string
}

type CreateReleaseOrder struct {
    IdempotencyKey  string
    ModelCode       string
    ReleaseTypeCode string
    Scope           Scope
    Description     string
    EffectiveFrom   *time.Time
    EffectiveUntil  *time.Time
    Items           []CreateReleaseItemInput
}

type ActOnReleaseOrder struct {
    OrderID             string
    ActionRequestID     string // UUIDv4
    ExpectedOrderRevision EntityRevision
    ExpectedCurrentStep string
    Action              ReleaseAction
    Comment             string
}
```

ADD 的 BaseBefore/EffectiveBefore 为空；DELETE 的 After 为空；MODIFY 的有效前态与后态非空，BaseBefore 可因 Overlay 而不同。PreserveSensitiveFields 只允许 MODIFY 且必须是模型中的 Sensitive 字段。每个 action_request_id 与规范请求摘要唯一绑定：同 ID 同请求返回原结果，同 ID 不同请求报 `IDEMPOTENCY_KEY_REUSED`，不同 ID 的旧 EntityRevision 报 Aborted。

## 10. Outbox 与 Audit

```go
type OutboxEvent struct {
    ID             string
    SequenceNo     int64
    AggregateType  string
    AggregateID    string
    EventType      string
    PayloadVersion int32
    Payload        []byte
    IdempotencyKey string
    Status         OutboxStatus
    LeaseRevision  LeaseRevision
    Attempts       int32
    NextAttemptAt  time.Time
    LockedBy       string
    LockedUntil    *time.Time
    LastError      string
    CreatedAt      time.Time
    UpdatedAt      time.Time
    PublishedAt    *time.Time
}

type AuditRecord struct {
    ID                   int64
    OccurredAt           time.Time
    PrincipalSubject     string
    PrincipalDisplayName string
    Action               string
    ResourceType         string
    ResourceID           string
    Scope                Scope
    Result               AuditResult
    Before               map[string]string
    After                map[string]string
    Metadata             map[string]string
    RequestID            string
    TraceID              string
}
```

Outbox Payload 在领域中是带 schema version 的稳定 bytes；MySQL adapter 映射为 JSON。Audit Before/After 进入该类型前已经按 CollectionDefinition 脱敏。
