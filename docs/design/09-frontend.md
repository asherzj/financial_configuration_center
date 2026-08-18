# 管理控制台前端详细设计

## 1. 产品目标

Admin Console 是面向配置管理员、发布操作人、审批人、审计员和运维人员的桌面管理端。默认中文，文案集中管理并预留英文；目标视口 1440×900，最低支持 1280 宽。

核心原则：配置编辑不是直接保存。

```text
查询配置 -> Add/Edit/Copy/Delete -> ChangeDraft
-> Diff 审查 -> 发布信息 -> 最终确认
-> ReleaseOrder -> 审批/执行/推进/比较/完成或回滚
```

### 1.1 分享页交互到开源抽象的映射

分享页《总结交互流程》给出的六屏主链路是：统一运营入口 → 配置列表查询 → 新增/编辑/复制/删除 → 字段 Diff → 填写发布信息 → 发布单分阶段推进。V1 保留这条交互骨架，但不复制历史系统的表名和企业术语：

| 分享页中的交互 | V1 页面/领域对象 | 保留的用户意图 | 不复制的内容 |
|---|---|---|---|
| 统一运营入口、查看详情 | `/operations` + ConfigurationModel selector | 一个入口路由到不同配置对象 | 每张表独立页面、历史表名 |
| 表格查询与定位 | QueryPage ALL/ONLY_DATA | 元数据驱动筛选、表格和分页 | PSM、业务表字段硬编码 |
| 新增/编辑/复制/删除 | ChangeDraft item | 熟悉的行级操作入口 | 直接 CRUD 生产记录 |
| 编辑弹窗点击确定 | Dynamic Record Form | 完成字段校验并形成候选变更 | 点击确定即写数据库 |
| 原值/新值红绿 Diff | Change Review Step | 人工确认真正改变的字段 | 敏感字段明文 Diff |
| 发布方式与备注 | Release Metadata Step | 选择适用 ReleaseType 和说明 | 固定“PPE/单机房”等内部发布方式 |
| 发布成功后跳转 | ReleaseOrder Detail | 进入可恢复、可审计的状态机 | 页面自行推导工作流 |
| 审批/监控/日志/回滚/继续 | Detail tabs + allowedActions | 在一个详情页完成发布操作与诊断 | 前端按步骤名硬编码按钮 |

历史页面的 PPE 验证、小流量、单机房、全流量只说明“步骤可配置、可分阶段推进”。V1 使用 TemplateSnapshot 渲染 stepper；demo 可以展示“人工复核 → 范围覆盖 → 对比验证 → 基础生效 → 完成”，生产模板可替换文案和组合，但前端行为不分叉。

### 1.2 主交互流程

```mermaid
flowchart LR
    A["统一操作入口"] --> B["选择 Model 与 Scope"]
    B --> C["ALL：查询控件、表格、发布类型"]
    C --> D["Add / Edit / Copy / Delete"]
    D --> E["浏览器 ChangeDraft"]
    E --> F["Step 1：字段 Diff"]
    F --> G["Step 2：发布类型与说明"]
    G --> H["Step 3：最终确认"]
    H --> I["Create ReleaseOrder"]
    I --> J["动态 Stepper 与 allowedActions"]
    J --> K["完成 / 在途回滚 / 成功后补偿发布"]
```

## 2. 技术栈

- Node 24 LTS、pnpm 11。
- React 19、TypeScript 5.9、Vite 8。
- Ant Design 6。
- React Router、TanStack Query。
- Ant Design Form + Zod：静态 HTTP envelope 和固定管理表单校验；动态字段仍由 ModelField 规则驱动。
- Vitest、Testing Library、MSW、Playwright。

V1 不使用 Redux/Zustand。服务器状态进 TanStack Query；路由状态进 URL；ChangeDraft 和 wizard 状态使用页面内 reducer/context。

## 3. 目录结构

```text
web/admin-console/src/
  app/                 # router、providers、error boundary
  api/                 # OpenAPI DTO/client、error mapping
  layouts/             # shell、navigation、scope switcher
  features/
    dashboard/
    operations/
    catalog/
    subscriptions/
    models/
    templates/
    releases/
    diagnostics/
    audit/
  components/          # 真正跨 feature 的通用展示模块
  auth/
  i18n/
  styles/
  test/
```

禁止按 `components/services/hooks/utils` 平铺整个项目，也禁止每个 feature 重写一套状态标签、错误页和确认弹窗。

## 4. 全局布局

- 左侧导航：仪表盘、统一操作入口、配置集合、消费者与订阅、配置模型、发布模板、发布单、运行诊断、审计日志。
- 顶栏：当前 Scope、server epoch/instance + snapshot generation/revision 提示、帮助、当前用户。
- Scope switcher：Region/Environment/Stage。生产 Environment 显著红/橙标识；切换时清空 ChangeDraft 并二次确认。
- 面包屑和页面标题包含资源稳定 code，不只显示中文名称。

前端角色切换只存在 demo mode，必须显示“演示身份”，不可在生产构建出现。

## 5. 路由

```text
/
/operations/:modelCode?
/collections
/collections/:name
/subscriptions
/models
/models/:code
/templates
/templates/:code/versions/:version
/releases
/releases/:id
/diagnostics
/audit
```

筛选条件、page、pageSize、scope 尽量序列化到 URL，刷新和分享可恢复。ChangeDraft、敏感值和 token 不进入 URL/localStorage。

## 6. 统一操作入口

桌面端页面骨架固定为四区，具体字段和列由 ALL 响应决定：

```text
┌──────────────────────────────────────────────────────────────┐
│ 面包屑 / Model 名称与 code / Scope / snapshot identity      │
├──────────────────────────────────────────────────────────────┤
│ 查询区：动态字段 + operator + 查询 / 重置 / 展开             │
├──────────────────────────────────────────────────────────────┤
│ 工具栏：新增 | 当前 ChangeDraft 数量 | 进入变更确认           │
│ 数据表：稳定主键 | 动态 Projection 列 | 状态 | 行操作          │
├──────────────────────────────────────────────────────────────┤
│ 分页 / page size / background refreshing 与 last-data 提示   │
└──────────────────────────────────────────────────────────────┘
```

### 6.1 初始加载

1. 选择 ConfigurationModel。
2. 发送 QueryPage `ALL`。
3. 根据 InteractionInfo 生成查询区、表格列和编辑表单。
4. 使用 Options 生成 SELECT；Disabled option 可显示但不能新选。
5. 展示 available/unavailable ReleaseTypes（不可用项解释 reason code）、数据、分页和 snapshot identity。

页面不得维护 model code → 字段列表的 hardcode map。

### 6.2 查询区

- 默认展开常用字段，超过 6 个可折叠。
- 控件由 UIControl 决定：INPUT/SELECT/TIME/NUMBER/BOOLEAN/TEXTAREA/JSON。
- operator 由 AllowedFilterOperators 决定；多个 operator 时明确展示切换。
- 查询、重置、展开/收起。
- 查询、分页、page size 变化调用 ONLY_DATA。
- 任一条件变化后 page=1。
- 请求期间保留旧表格但显示 loading；失败保留旧结果并显示可重试错误，不清空成“无数据”。

### 6.3 数据表

- 列由 ProjectionFields/InteractionInfo 驱动，稳定主键列固定在左侧，操作列固定右侧。
- 行操作：新增（页级）、编辑、复制、删除。
- Copy 使用当前 row 作为初值，但清空/要求修改 KeyFields。
- Delete 只生成 ChangeDraft item，不立即删除。
- Sensitive 默认显示掩码；有权限用户点击临时查看调用受审计 reveal endpoint。值只保存在当前 cell/modal 内存，关闭或 60 秒后清除，不进入 Query cache、URL、localStorage 或日志。
- server epoch/instance 或 generation 变化时提示“配置已更新，已回到第一页”，重新 ALL；generation 不跨 instance 比较，不继续使用旧分页。

## 7. 动态编辑表单

- Editable=false 只读。
- Required、默认值、类型、长度、枚举等提供即时校验，但服务端仍是权威。
- TIMESTAMP 使用带时区说明的时间控件，提交 UTC RFC3339Nano。
- JSON 使用格式化 textarea/editor，保存前解析并稳定格式化。
- 自动填充字段只读并提示“提交时由服务端生成”。
- 已有 Sensitive 字段默认显示“保持原值”；用户可显式选择“替换”并输入新值。未替换字段写入 preserveSensitiveFields，不把掩码当作 after 值。
- 修改 KeyField 不作为普通 MODIFY；UI 提示将拆成 DELETE + ADD，并要求重新确认。

确认编辑只把 item 加入浏览器 ChangeDraft，关闭弹窗后表格行显示“待发布”标识和 before/after 预览。

## 8. ChangeDraft 与三步确认

ChangeDraft reducer 保证同一 `(collection,recordKey)` 最多一个 item，并合并可消除操作：新增后删除直接移除 draft；连续修改合并为首次 before + 最终 after。

### Step 1：变更确认

- 展示 item 列表、ADD/MODIFY/DELETE 标签、目标描述。
- 字段级 Diff 同时展示 EffectiveBefore → After；当 BaseBefore 不同于 EffectiveBefore 时增加“基础前态”列，避免用户误解最终效果。旧值红色、新值绿色，未变字段默认折叠。
- Sensitive 只显示掩码和“已变化”，不显示真实 Diff。
- 允许返回编辑或移除 item。

### Step 2：发布信息

- ReleaseType 来自 QueryPage ALL。
- Scope 明确展示，生产环境高风险提示。
- 发布说明必填，显示将采用的 template 名称/版本预览。

### Step 3：最终确认

- 汇总 model、scope、item 数、动作分布、发布类型、说明。
- 生产发布要求输入 model code 或发布确认短语。
- 使用 `crypto.randomUUID()` 生成 UUIDv4 idempotency key，提交中禁用重复点击。
- 成功清空 draft 并跳转 ReleaseOrder detail。
- RevisionConflict 不清空 draft，重新 ALL 后展示“前态已变化”，要求重做 Diff。

## 9. ReleaseOrder 列表

筛选：release number、model、scope、status、creator、时间范围。稳定排序默认 updatedAt DESC + id。

表格展示：编号、模型、Scope、发布类型、状态、当前步骤、items、创建人、更新时间。状态使用文字+颜色，不仅靠颜色。

## 10. ReleaseOrder 详情

详情页沿用分享页的“进度 + 信息 + 固定操作区”结构，但由 TemplateSnapshot 和服务端 allowedActions 驱动：

```text
┌──────────────────────────────────────────────────────────────┐
│ ReleaseNumber / Status / Model / Scope / Creator / Revision  │
├──────────────────────────────────────────────────────────────┤
│ TemplateSnapshot 动态 Stepper                                │
├──────────────────────────────────────────────────────────────┤
│ Tabs：变更 | 审批 | 版本范围 | 监控诊断 | 操作日志 | 审计     │
├──────────────────────────────────────────────────────────────┤
│ 固定操作栏：comment / 二次确认 / allowedActions              │
└──────────────────────────────────────────────────────────────┘
```

### 10.1 顶部

- release number、status、model、scope、creator、description、revision。
- 横向 Stepper，步骤来自 TemplateSnapshot：常见顺序 MANUAL_REVIEW → OVERLAY_APPLY/PERCENT_ROLLOUT → BASE_APPLY → COMPARE → COMPLETE，但不得写死。
- 当前步骤、失败步骤和已回滚步骤有清晰状态。

### 10.2 Tabs

- 变更内容：Items 和字段 Diff。
- 审批记录：ApprovalState、评论和 actor。
- 版本与范围：before/current revision、digest、Overlay、percentage ranges。
- 监控与诊断：安全展开的 ObservabilityLinks、COMPARE 结果、preview bucket。
- 操作日志：ReleaseOperationLog 时间线。
- 审计：与 release 关联的 AuditRecords。

### 10.3 固定操作栏

按钮完全使用响应 `allowedActions`，可能包含提交审批、批准、拒绝、执行、推进、回滚、创建补偿发布。前端不得根据 step name 自行推导权限或状态。

- 高风险动作使用 Modal，不使用轻量 Popconfirm。
- APPROVE/REJECT/ROLLBACK 要求 comment。
- 每次 action 生成 UUIDv4 actionRequestID，并与 expectedOrderRevision/currentStep 随请求提交；网络重试复用同一 ID，用户再次操作生成新 ID。
- Aborted 后刷新详情并明确提示另一操作已推进。
- IN_PROGRESS 每 3 秒轮询；终态停止。页面不可见时暂停。

## 11. 元数据页面

### Collections

列表、详情、字段契约、KeyFields、SDK 分发/状态、revision。破坏性 schema 变更 UI 直接禁用并解释需新建 Collection。

### Consumers & Subscriptions

Consumer、Collection、IndexName、IndexFields、Cardinality、Enabled。ONE_TO_ONE 显示唯一性风险说明。

### Models

编辑 Fields、QueryPageDefinition、Projection、Sort、OptionSource、ReleaseTypes 和 AutoFill。保存前提供“模型编译预检”，错误精确定位字段路径。

### Templates

只创建新 version。步骤拖拽后仍显示 sequence，参数使用 step type 专用表单，禁止通用 JSON/脚本编辑器。发布前显示 rollback chain 验证结果。

## 12. Diagnostics 与 Audit

Diagnostics：server epoch/instance、snapshot instance/generation、publishedAt、各 collection revision/cursor/digest、最近 refresh 结果、失败集合/依赖组、Watch 数、outbox/dead-letter 摘要。只读为默认。

Audit：按时间、actor、action、resource、result、scope 筛选；before/after 字段级脱敏。导出不属于 V1，避免未定义的数据泄漏通道。

## 13. 通用状态

每个页面必须实现：initial loading、background refreshing、empty、permission denied、not found、validation error、revision conflict、unavailable with last data、fatal error。

错误展示包含可复制 TraceID；不展示 stack、SQL 或内部 endpoint。危险按钮按权限隐藏，同时服务端继续鉴权。

## 14. 可访问性与视觉

- Ant Design token 统一颜色、间距、圆角和状态色；不在 feature 写散乱 magic color。
- 键盘可完成查询、表单、wizard 和 modal；焦点返回触发按钮。
- 表单错误与字段关联；图标有 accessible label。
- Diff 除红绿外同时使用 +/-、旧/新标签。
- 生产 Scope 使用持续可见的环境标识，不只弹一次警告。

## 15. 前端测试

### Vitest/Testing Library

- QueryPage metadata → 控件/列映射。
- ALL/ONLY_DATA 调用切换和 generation reset。
- ChangeDraft 合并、Diff、三步 wizard、revision conflict 保草稿。
- allowedActions 按钮矩阵。
- Sensitive masking、错误状态、生产确认。

### MSW contract

fixtures 从 OpenAPI 示例生成或校验；禁止手写与契约漂移的 mock shape。

### Playwright

1. 进入 Model，ALL 加载。
2. 筛选/翻页走 ONLY_DATA。
3. Edit → Diff → 发布信息 → 创建 ReleaseOrder。
4. APPROVE → EXECUTE → ADVANCE → COMPLETE。
5. Overlay/percentage preview 和 rollback。
6. Revision conflict、PermissionDenied、Unavailable last data。

## 16. 前端完成标准

- 不存在 model-specific 页面分支即可展示 demo Models。
- 所有记录变更都经过 ChangeDraft/ReleaseOrder。
- 刷新页面可从 URL 恢复查询，但不会恢复敏感草稿。
- `pnpm lint`、typecheck、unit、Playwright 和 production build 通过。
- production bundle 不包含 dev role switch、token 或私有历史术语。
