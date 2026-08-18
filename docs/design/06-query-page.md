# QueryPage 低代码查询详细设计

## 1. 模块目标

PageQuery 模块用一份 ConfigurationModel 驱动管理页面的字段、筛选、选项、发布类型和分页数据。它不接受 SQL、表名、列名、JOIN、表达式 eval、脚本或任意 URL。

外部接口保持一个方法：

```go
type PageQuerier interface {
    Query(context.Context, Principal, PageQuery) (PageResult, error)
}
```

内部封装模型编译、权限、类型转换、effective record、过滤、排序、分页、选项、投影、脱敏和容量保护。

## 2. 模型定义

### QueryPageDefinition

- Enabled。
- ProjectionFields：非空，决定 row 输出字段。
- DefaultSort：非空，最后总是追加 RecordKey ASC。
- DefaultPageSize：默认 20。
- MaxPageSize：默认 100，系统 hard max 200。
- AllowedQueryTypes：ALL/ONLY_DATA。

### ModelField

- Name/DisplayName/DisplayOrder。
- Type、Required、Sensitive、DefaultValue、ValidationRules，必须与 CollectionDefinition 一致。
- Editable、Queryable。
- UIControl：INPUT/SELECT/TIME/NUMBER/BOOLEAN/TEXTAREA/JSON。
- AllowedFilterOperators。
- OptionSource 可空；SELECT 必填。

### OptionSource

- STATIC：内嵌 Code/Label/Disabled，Code 唯一。
- COLLECTION：引用已注册 Collection、ValueField、LabelField、FixedFilters、Sort、Limit。
- 不允许跨网络、SQL 或 model-specific resolver。

## 3. 模型编译

Model 启用前由 `ModelCompiler` 完整编译：

1. Collection 存在且 enabled。
2. ModelField Name/DisplayOrder 唯一，字段存在且共享属性一致。
3. Projection、Sort、ReleaseKey/Description、AutoFill 只引用模型字段；ReleaseKey/Description，以及 COLLECTION OptionSource 的 value/label/fixed filters/sort 均不得引用目标 Collection 的 Sensitive 字段。
4. Queryable=false 字段无 operator。
5. Sensitive 在 V1 不得 Queryable 或 Sort；可以出现在 ProjectionFields，但 PageRow 只返回 masked marker，不返回明文。
6. 操作符与类型兼容：
   - STRING：EXACT、CONTAINS、IN、NOT_IN；
   - INT64/FLOAT64/TIMESTAMP：EXACT、CLOSED_RANGE、OPEN_RANGE、IN、NOT_IN；
   - BOOL：EXACT、IN、NOT_IN；
   - JSON：EXACT、IN、NOT_IN，禁止 Sort；
7. UIControl 与类型兼容；SELECT 必须有 OptionSource。
8. COLLECTION option 的字段和固定条件在目标 CollectionDefinition 中合法。
9. DefaultSort 至少一项，禁止 Sensitive/JSON。

编译产物不可变，可按 `(model revision, referenced collection revisions)` 缓存。缓存删除不改变行为。

## 4. PageQuery

```go
type PageQuery struct {
    ModelCode    string
    Scope        Scope
    QueryType    QueryPageType
    Conditions   []FilterCondition
    Page         PageSpec
    PreviewBucket *int32
}
```

`PreviewBucket` 只供有权限的发布诊断使用，范围 0..99；普通管理查询为空时不应用 percentage Overlay，只应用无 bucket selector 的 Overlay。Admin Console 的发布详情可显式选择预览 bucket。Go SDK 的真实 bucket 不由 QueryPage 推断。

PageResult 的行使用明确结构，而不是裸 `map`：

```go
type PageRow struct {
    RecordKey      string
    RecordRevision ConfigRevision
    Values         map[string]string
    MaskedFields   []string
}
```

RecordKey/RecordRevision 用于构造 ReleaseItem 和 optimistic concurrency；表格是否显示由 UI 决定。Values 只包含非敏感 ProjectionFields；存在值的敏感字段只出现在稳定排序的 MaskedFields 中。明文必须通过受审计 SensitiveAccessService 单字段获取。

## 5. Transport 条件编译

adapter 把 transport 字符串映射为领域 ScalarValue：

- EXACT/CONTAINS 必须且只能有 Value。
- RANGE 至少一个 Lower/Upper；CLOSED_RANGE 使用 `>= <=`，OPEN_RANGE 使用 `> <`。
- IN/NOT_IN Set 非空、去重后最多 100。
- CONTAINS 只对 STRING，按 Unicode code point 子串匹配；`%`、`_` 是普通字符。
- nil condition、未知字段/operator、溢出、NaN/Inf、非法时间/JSON、下界大于上界整体 InvalidArgument。

SELECT 条件值必须存在于当前 OptionSource 且未 Disabled。ONLY_DATA 为完成验证仍可读取/编译选项，但不把 options 返回响应。

## 6. 单快照执行流程

请求入口只读一次 SnapshotManager.Current 指针：

1. 校验 Principal 对 Model、Collection、Scope 的读取权限。
2. 加载并编译 Model + QueryPageDefinition。
3. 编译条件和分页；PageSpec 省略 number 时为 1、省略 size 时为模型默认，显式非正值 InvalidArgument。
4. 从同一 snapshot 取得目标 Environment 的基础 records 和 overlays。
5. 计算 Scope/PreviewBucket 下 EffectiveRecords。
6. 按领域值过滤；Sensitive 字段在 V1 不可过滤或排序，消除条件侧信道。
7. DefaultSort + RecordKey ASC 稳定排序。
8. 计算 total，规范页码并切片。
9. 按 ProjectionFields 投影和脱敏。
10. ALL 构造 interaction fields、options、enabled release types；ONLY_DATA 跳过响应装配。
11. 返回 server epoch、instance、snapshot instance/generation 与 model/collection ConfigRevision。

任一步失败不返回半套 interaction/options/data。

## 7. 排序与分页

- 数值、BOOL、TIMESTAMP 按领域类型比较；STRING 按 UTF-8 code point；JSON 不排序。
- 字段缺失始终排在有值之后，不随 ASC/DESC 翻转。
- 多个 sort term 依次比较，最终 RecordKey ASC。
- Page number 省略使用 1；显式 <=0 返回 InvalidArgument。
- Size 省略使用 DefaultPageSize；<=0 或超过 MaxPageSize 返回 InvalidArgument。
- total=0 时 PageNumber=1、TotalPages=0。
- 请求页超过 TotalPages 时规范到最后一页。
- ServerEpoch、ServerInstanceID 或 SnapshotGeneration 与上次不同由前端重置页码；Generation 不跨 instance 比较大小，服务端不承诺跨 snapshot identity 的 offset pagination 连续性。

## 8. InteractionInfo

ALL 返回字段按 `(DisplayOrder, Name)` 排序：Name、DisplayName、Description、Type、UIControl、Queryable、Editable、Required、Sensitive、Projected、KeyField、AutoFill、完整 AllowedFilterOperators、DefaultFilterOperator、ValidationRules、DefaultValue 和 Options。PageResult 另返回稳定排序的 ProjectionFields；前端不得靠字段存在与否猜表格列。

字段可以作为查询控件但不在 ProjectionFields 中；此时 row 不输出该字段。Sensitive 字段只作为 masked column 展示，不生成查询控件；SENSITIVE_VIEWER 也必须通过受审计 reveal 读取单值。

## 9. Option 解析

STATIC 在模型编译时去重排序。COLLECTION option：

1. 从同一 snapshot 读取目标 collection；刷新协调器保证当前 Collection 与 OptionSource 依赖闭包 all-or-nothing。
2. 应用请求 Scope 下无 percentage selector 的 Overlay。
3. 应用受管 FixedFilters。
4. 读取 ValueField/LabelField；字段缺失或类型不兼容使整个 ALL 失败。
5. 按定义 Sort，最后 Value ASC。
6. Code 去重；同 code 不同 label 是模型损坏。
7. 超过 Limit 或 hard max 1,000 失败，不截断。

禁止每字段网络调用或数据库 N+1。

## 10. 容量限制

- 单模型字段 100；
- 条件 20；
- IN/NOT_IN 元素 100；
- candidate records 100,000；
- options 每 source 1,000；
- page hard max 200；
- BFF JSON 响应 4 MiB。

达到上限使用 ResourceExhausted；参数自己超过声明上限使用 InvalidArgument。

## 11. 错误语义

- Model/Collection 不存在：NotFound。
- disabled、QueryPage 未启用、坏 option 引用：FailedPrecondition。
- 条件、分页、类型：InvalidArgument。
- 无权限：PermissionDenied。
- 无 snapshot：Unavailable。
- snapshot 内不变量损坏：Internal，同时保留服务 last-known-good。

## 12. 前端契约

- 页面首次进入/切模型调用 ALL。
- 查询、清空、翻页、page size 变化调用 ONLY_DATA。
- ALL 的 InteractionInfo 是控件和表格元数据真相；前端不维护 model-specific 字段表。
- Disabled option 可显示历史值但不可提交新查询或发布。
- snapshot identity 变化显示“配置已更新，已回到第一页”并重新 ALL。

## 13. 测试表面

- ALL/ONLY_DATA、空结果、默认和超界分页。
- 每种字段/操作符、缺失值、Unicode、时间和 JSON 规范化。
- Overlay ADD/MODIFY/DELETE 与 PreviewBucket。
- 稳定排序和 generation 变化。
- STATIC/COLLECTION options、坏引用、重复 code、Disabled。
- Sensitive projection/filter/sort 侧信道。
- 容量上限、并发 snapshot swap、last-known-good。
