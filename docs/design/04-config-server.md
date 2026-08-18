# 配置读取服务详细设计

## 1. 模块目标

Config Server 把 MySQL 中的可变事实转换为可并发读取的不可变 `ConfigurationSnapshot`，并提供 SDK 读取、版本比较、Watch 和 QueryPage。其核心模块是 `SnapshotManager`；删除该模块后，刷新、校验、压缩、游标、部分失败和原子发布复杂度会泄漏到所有 handler，因此该模块必须保持深接口。

## 2. 进程组成

`cmd/config-server` composition root 构造：

- 只读 MySQL/GORM adapter；
- SnapshotManager；
- ConfigService 与 PageQueryService Kitex handler；
- VersionPoller、RefreshHint receiver、Overlay boundary 状态观察器；
- WatchHub；
- health/ready/metrics HTTP server；
- 根 context、errgroup 和优雅关闭。

启动顺序固定：配置校验 → DB capability/ping → FULL refresh → 发布首个 snapshot → ready=true → 启动 RPC/Watch/poll。首个 FULL refresh 失败则启动失败；启动后刷新失败保留 last-known-good。

## 3. Snapshot 结构

```go
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

所有 slice 在构建阶段稳定排序，所有 map value 深拷贝。`Current()` 返回只读指针；包外没有 setter，不提供返回内部可变 map 的方法。

SnapshotManager 使用：

- `atomic.Pointer[ConfigurationSnapshot]` 服务 reader；
- 一个 refresh mutex 串行化 writer；
- 合并队列把并发 RefreshRequest 按 collection 和最小目标 revision 合并；
- refresh context 有总超时和单集合超时。

## 4. SnapshotManager 接口

```go
type SnapshotManager interface {
    Current() *ConfigurationSnapshot
    Refresh(context.Context, RefreshRequest) (RefreshResult, error)
}
```

不暴露 `ReloadDefinitions/ReloadRecords/Compress/Swap` 等浅方法。测试和 handler 只能通过 `Refresh` 观察行为。

## 5. 刷新模式

### 5.1 FULL

在一个只读 REPEATABLE READ 事务中加载全部 enabled definitions、subscriptions、models、versions、records、active/pending overlays 和每集合 max change cursor。构建、校验、摘要复核、压缩全部成功后发布 generation+1。

### 5.2 COLLECTIONS

完整重载指定集合的 definition、records、subscriptions、models、overlays、versions 和 cursor，其他集合结构共享旧 snapshot 的只读值。候选顶层 map 必须复制，不能在旧 map 上写。

### 5.3 OVERLAYS

重载目标集合的 Overlay、Version 和受影响压缩/派生内容。若数据库 version 表明 definition/model 也变化，自动升级为 COLLECTIONS。

### 5.4 INCREMENTAL

1. 从旧 snapshot 获取每集合 cursor。
2. 按集合读取 `(cursor, upper_cursor]` 的 change log，单批最多 1,000 条。
3. 聚合 record keys/kinds；After 仅用于诊断。
4. 在同一只读事务批量回查最终 records、overlays、metadata 和 version。
5. copy-on-write 构建并完整校验目标集合。
6. 成功集合推进到 upper cursor；失败集合保持旧 cursor。
7. 变更量超过集合行数 20% 或单批上限时降级 COLLECTIONS。

### 5.5 VERSION_POLL

每个实例默认每 30 秒读取轻量 `configuration_versions` 和 metadata revision。发现大于本地 revision 时触发 COLLECTIONS/OVERLAYS；poll 加 ±20% jitter。该路径保证 RefreshHint、outbox relay 或 Watch 全部丢失后仍最终收敛。

## 6. 部分成功与依赖组规则

多个 collection 的 refresh 相互独立时允许部分成功：

- 成功集合进入新候选 snapshot。
- 失败集合的 definition/data/version/overlay/compressed-base/cursor 整组保留旧值。
- 至少一个集合成功才发布 generation+1。
- 全部失败时不发布、不广播，返回聚合错误。
- 从 enabled Model 的 COLLECTION OptionSource 建立 collection dependency graph。一次事务中共同变化的连通依赖闭包作为 dependency group all-or-nothing 构建；组内任一集合失败，整组 definition/model/data/version/compressed-base/cursor 保留旧值。无依赖集合仍允许独立部分成功。
- QueryPage ALL 的 FailedPrecondition 只作为已有损坏数据的防线，不作为正常部分刷新策略。

同一集合内部禁止部分更新。

## 7. GetSnapshot

请求必须包含 ConsumerID、ClientID、Scope 和可选 known versions。处理步骤：

1. 认证 Consumer，入口读取一次 snapshot。
2. 找到 enabled Subscriptions 和允许集合。
3. 按 known versions 计算需要 ADD/MODIFY/DELETE 的集合。
4. 使用 ConsumerID + ClientID 的稳定 SHA-256 bucket `0..99` 过滤 PERCENT_ROLLOUT Overlay；普通 Overlay 不过滤。协议算法固定为 SHA-256(`UTF8(consumer_id) || 0x00 || UTF8(client_id)`)，取摘要前 8 字节按 big-endian uint64 后 `% 100`。
5. 为目标 scope 构造 payload；不得把无权集合或其他 scope 敏感内容发给 SDK。
6. 对筛选后的 EffectiveRecords 计算响应级 EffectiveDigest，并返回 server epoch/instance、snapshot instance/generation、版本、cursor 和 payload。

ClientID 必填且在一个安装实例生命周期内稳定。bucket 算法是协议的一部分，必须 contract test；禁止 Go `hash/maphash` 等进程随机 hash。

压缩默认 GZIP，序列化格式为稳定 protobuf bytes；同样输入必须产生相同解压内容，压缩字节本身不参与业务 digest。CollectionVersion.OverlayDigest 仍覆盖 Environment 全部 Overlay，SDK 使用响应级 EffectiveDigest 校验自己实际收到的 bucket 视图。

## 8. DiffVersions 与 GetCollections

DiffVersions 输出互斥、去重、按 collection 排序的 Add/Modify/Delete：

- 客户端无、服务端有：ADD。
- 双方有且 revision/digest 任一不同：MODIFY。
- 客户端有、当前 Subscription 无：DELETE。

GetCollections 只允许请求当前 Consumer 已订阅的集合，并复用 GetSnapshot 的 scope/bucket 过滤。请求的 min revision 高于当前 snapshot 时触发/加入目标 refresh，并最多等待 2 秒；达到目标后返回，否则返回 Unavailable。不得返回较旧数据伪装成功。

## 9. RefreshHint 与 Outbox

Control Plane commit 后由 outbox relay best-effort 调用 Config Server 内部 refresh endpoint。Hint 包含 EventID、目标集合、Kinds、MinRevision、TargetCursor、Scope、ReleaseOrderID、TraceID。

- EventID 使用有界 TTL cache 去重。
- 小于等于当前 revision/cursor 的旧 Hint 直接确认。
- 乱序 Hint 合并成每集合最大目标水位。
- 接收成功只表示已入 refresh queue，不表示 snapshot 已达到目标。
- 队列满返回 ResourceExhausted，relay 重试；version poll 仍兜底。

## 10. WatchHub

WatchHub 在 snapshot 成功发布后接收 UpdateEvent。每个 subscriber 有有界队列，默认 64：

- 同 collection 未发送事件合并为最新 revision。
- 队列仍溢出时发送 `RESYNC_REQUIRED` 后关闭 stream。
- 慢客户端不得阻塞 refresh writer 或其他 subscriber。
- 首条事件包含当前 generation 和该 Consumer 可见版本水位。
- 每条事件携带 server epoch/instance；epoch 变化强制 FULL，instance 变化时 generation 只作为新实例内观察值。
- 心跳默认 20 秒，不携带配置正文。

连接断开不是错误日志洪水；按原因分类 metric。WatchHub 只持有订阅元数据，不持有可变 snapshot 副本。

## 11. 容量限制

默认硬上限，可通过配置调低但不能调高超过编译常量：

- 单 collection 100,000 records；
- 单 record 256 KiB 规范 JSON；
- 单 collection 未压缩 64 MiB；
- 单 GetSnapshot 响应 128 MiB；
- 单 incremental batch 1,000 log entries；
- Watch 每 Consumer 1,000 个并发连接，全实例总数另配。

达到上限使目标集合刷新失败并保留旧值；不得截断配置集合。

## 12. 健康与关闭

- `/healthz`：进程 event loop 活着。
- `/readyz`：已有可用 snapshot、MySQL 最近一次 ping 在阈值内、RPC 正在接受请求。
- MySQL 短暂失败不清空 ready；超过配置阈值后 ready=false，但继续服务 last-known-good 读取。
- 关闭时先 ready=false，停止接新流，取消 poll/refresh，等待在途请求，最后关闭 DB/HTTP/RPC；默认 30 秒。

## 13. 接口级测试

- 初始 FULL、权威空集合、加载失败。
- 每种 refresh mode、增量最终态回查和降级。
- 单集合失败保旧 cursor，其他集合仍发布。
- 同时 reader + refresh 的 race test，无撕裂状态。
- Hint 重复/乱序/丢失，version poll 最终收敛。
- Watch 慢客户端、队列满、重连首事件。
- Consumer/Scope/Client bucket 权限与过滤。
- 关闭期间无 goroutine/stream 泄漏。
