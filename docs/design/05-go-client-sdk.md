# Go 客户端 SDK 详细设计

## 1. 对外目标

SDK 作为独立可 import 包 `client`，在业务进程内提供无网络热路径的结构化配置查询。所有状态属于 Client 实例，禁止 package 级可变单例。

## 2. 对外接口

```go
func New(options ClientOptions) (*Client, error)

func (c *Client) Start(ctx context.Context) error
func (c *Client) Close(ctx context.Context) error
func (c *Client) QueryOne(q Query) (ReadonlyRow, bool, error)
func (c *Client) QueryMany(q Query) ([]ReadonlyRow, error)
func (c *Client) QueryAll(region, collection string) ([]ReadonlyRow, error)
func (c *Client) Version(region, collection string) (VersionView, bool, error)
func (c *Client) Subscribe(collection string, handler UpdateHandler) (CancelFunc, error)
```

未命中统一使用 `found=false, err=nil` 或空 slice，不使用错误表达合法空结果。参数、生命周期、集合和索引错误使用可 `errors.Is/As` 的 typed errors。

泛型 decode 作为包函数或 Client 方法提供：

```go
func Decode[T any](row ReadonlyRow) (T, error)
func DecodeMany[T any](rows []ReadonlyRow) ([]T, error)
```

Decode 使用显式、可测试的字段转换，不通过 GORM/json tag 推断领域语义。

## 3. ClientOptions

```go
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
```

ConsumerID、ClientID、DefaultRegion 必填。ClientID 必须跨进程重启稳定，用于百分比 rollout bucket；不得每次启动生成随机值。

每个 RegionOptions 声明 Endpoint、Scope、Required、WatchEnabled、PollInterval、RequestTimeout、ReconnectBackoff 和 TLSProfile。region 名称来自配置，不硬编码双 region。

## 4. 生命周期状态机

```text
NEW -> STARTING -> RUNNING -> CLOSING -> CLOSED
          |          |
          v          v
        FAILED <-----+
```

- Start 只允许从 NEW/FAILED 进入；并发 Start 返回 ErrAlreadyStarted。
- Required region 初始失败使 Start 失败并清理已创建资源。
- 非 Required region 失败被记录，后台继续恢复；至少一个 required region 成功才可 RUNNING。
- Close 幂等；CLOSING 后拒绝 Subscribe 和刷新，已有只读查询可继续读最后 snapshot 直到 CLOSED。
- 所有后台循环派生自一个根 context，并由 WaitGroup/errgroup 汇合。

## 5. ClientSnapshot 与紧凑存储

每个 region 持有独立 `atomic.Pointer[ClientSnapshot]` 和 refresh mutex。

TableStore：

- Columns/ColumnIndex：字段字典；
- Values：字符串驻留池，0 保留给“字段缺失”；
- RowValues + CompactRowMeta：连续行存储；
- Primary：RecordKey → rowID；
- SecondaryIndex.One/Many；
- WholeCollectionRowIDs：按 RecordKey 稳定排序。

ReadonlyRow 只暴露：

```go
Get(field string) (string, bool)
Len() int
Range(func(field, value string) bool)
CloneMap() map[string]string
```

内部 store 和 rowID 不导出。旧 snapshot 在查询返回前因局部指针保持可达；新 refresh 只 atomic swap，不修改旧 store。

## 6. 初始加载

每个 region：

1. 创建 Kitex gRPC client。
2. 调 GetSnapshot，携带 ConsumerID、ClientID 和 Scope。
3. 校验 collection、definition、subscription、version、payload 数量和名称一致。
4. 解压并验证 format version、record key、字段规则和 BaseDigest。
5. 应用服务端按 Client bucket 筛选后的 Overlay，计算并验证 EffectiveDigest，再构建全部 TableStore 和 secondary indexes。
6. 完整成功后 atomic store。
7. 启动 Watch 和 version poll。

不得先发布空 snapshot 再逐 collection 填充。

## 7. 查询语义

### QueryOne

- RecordKey 查询走 Primary。
- Index 查询只允许 ONE_TO_ONE；ONE_TO_MANY 返回 ErrCardinalityMismatch。
- 入口只加载一次 snapshot。
- 期望 O(1)，不允许回退全表扫描。

### QueryMany

- ONE_TO_ONE 返回 0..1 条，ONE_TO_MANY 返回稳定 rowID 序列。
- Limit < 0 非法；0 表示不额外限制但仍受 SDK hard limit 10,000。
- 返回新 slice，不暴露 SecondaryIndex 内部 slice。

### QueryAll

- 通过 WholeCollectionRowIDs 保证稳定顺序。
- 权威空集合返回空 slice。
- 超过 100,000 行返回 ErrResultTooLarge，不部分返回。

### Version

只读本地 VersionView，不触发网络。结果与同 snapshot TableStore 一致。

## 8. 刷新与自愈

Watch event、poll tick 和手动 recovery 最终都进入同一个 per-region refresh coordinator：

1. 合并目标最小 revision。
2. 调 DiffVersions。
3. 批量 GetCollections。
4. 私有构建 Add/Modify collection，处理 Delete。
5. 校验完整批次。
6. atomic store 新 ClientSnapshot。
7. 发布本地 UpdateEvent。

任一 Add/Modify 构建失败时拒绝整批并保留旧 region snapshot；客户端不做 Config Server 的跨 collection 部分成功，因为 RPC 响应已是一个声明的一致批次。

Watch 断开使用指数退避 + jitter 重连；poll 持续。后台 panic 必须 recover、记录 stack，并触发 FULL recovery，不清空 last-known-good。

## 9. 本地订阅

- UpdateEvent 类型 DATA_CHANGED/SUBSCRIPTION_CHANGED。
- event 只在 snapshot store 后产生。
- at-least-once、允许重复/合并，不承诺跨 collection 全局顺序。
- callback worker 数默认 4、最大 64；队列默认 256、最大 4096。
- 队列满时按 `(region,collection,event type)` 合并到最新 revision；仍无法入队则丢弃并计数，调用方可随时查询当前 snapshot 恢复。
- 执行用户 handler 时不持注册/refresh 锁；panic recover，慢 handler 不阻塞其他 handler。

## 10. Percent rollout

服务端依据 ConsumerID + ClientID 计算稳定 bucket 并只返回命中的 percentage Overlay。算法固定为 SHA-256(`consumer_id || 0x00 || client_id`) 前 8 字节 big-endian `% 100`。SDK 可以独立复算用于诊断，但不自行选择不同 bucket，也不允许调用方在查询时伪造 bucket。

为了诊断可暴露只读 `Bucket() int`，其返回固定 0..99，不包含 hash 输入。改变 ClientID 会改变 rollout 归属，属于部署配置变更。

## 11. 错误类型

至少定义：ErrNotStarted、ErrClosed、ErrRegionNotFound、ErrCollectionNotFound、ErrIndexNotFound、ErrCardinalityMismatch、ErrInvalidQuery、ErrCorruptSnapshot、ErrUnsupportedFormat、ErrResultTooLarge。

错误消息可含 region/collection/field 名，不含字段值和配置正文。

## 12. 可观测性

SDK 默认不启动 exporter；接受可选 slog Logger、OTel TracerProvider/MeterProvider 或轻量 Observer。库不得修改全局 logger/provider。

记录 region、collection、generation、revision、RPC/解压/构建耗时、memory estimate、watch reconnect、poll recovery、callback lag/panic；不记录查询 values 或 row content。

## 13. 测试表面

- 生命周期、required/optional region、失败重试、幂等 Close。
- 紧凑存储空值/缺失、Unicode、索引碰撞、ONE_TO_ONE 冲突。
- QueryOne/Many/All/Version 的稳定结果和 typed errors。
- Refresh Add/Modify/Delete、坏 payload 保旧、响应丢失重试。
- Watch 丢失/重复/乱序，poll 最终恢复。
- callback panic/慢/队列满，不阻塞 refresh。
- ClientID bucket 稳定性。
- `go test -race` 和 goroutine leak tests。
