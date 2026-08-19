# 金融配置中心

> 面向大型互联网金融公司的企业级配置中心。

`financial_configuration_center` 聚焦金融级配置治理，为复杂业务系统提供统一、可靠、安全的配置管理能力。项目以大型互联网金融场景中的高可用、强管控、可审计和多环境治理需求为设计目标。

## 核心价值

- **金融级可靠性**：配置读取高可用，支持本地缓存、容灾和快速恢复
- **企业级治理**：统一管理多应用和多环境配置；第一版不引入隐式多租户语义
- **安全与合规**：细粒度权限、审批流程、操作审计和敏感配置保护
- **变更可控**：草稿、发布、灰度、回滚和版本对比
- **结构化管理**：通过表、字段和记录表达复杂金融业务配置
- **开放集成**：提供 SDK、HTTP API、事件订阅和生态扩展能力

## 典型场景

- 支付渠道与路由配置
- 产品参数和费率配置
- 交易限额配置
- 风控规则参数
- 机构、币种和地区配置
- 营销活动与权益配置

## 结构化配置

金融业务配置往往不是简单的 `key-value`，而是具有明确字段、类型、约束和关联关系的数据。第一版通过受管的 `ConfigurationCollection` 和 `ConfigurationModel` 表达这些结构，通过字段白名单、类型化条件和稳定分页查询，不接受任意 SQL、表名、JOIN 或脚本。

配置变更不会直接修改记录。管理控制台先形成字段级 Diff，再创建 `ReleaseOrder`，经过审批和分阶段发布后才产生配置效果。

## 目标能力

- 配置表、字段、记录和约束管理
- Region/Environment/Stage 多环境 Scope 隔离
- 草稿、审批、发布、灰度和回滚
- 完整的版本历史与审计日志
- RBAC 权限和敏感配置保护
- Go SDK、Kitex RPC、Admin BFF HTTP 接口和变更订阅
- 高可用读取、缓存与容灾
- 声明式、模型驱动的配置查询和受控变更

## 项目状态

项目处于设计阶段，面向大型互联网金融场景进行能力规划。当前尚未声明任何生产使用案例。

V1 的冻结技术基线、整体架构、分模块详细设计和编码任务包见 [设计文档入口](./docs/design/README.md)。编码实现与本文愿景发生冲突时，以该设计包为准。

## 初始化演示元数据

先对空 MySQL 8.4（兼容 8.0）数据库运行 Goose migration，再执行可重复的 seed 命令：

```sh
export FINCONFIG_MYSQL_DSN='finconfig:password@tcp(127.0.0.1:3306)/finconfig?parseTime=true&loc=UTC&multiStatements=true'
go run ./cmd/migrate -command up
go run ./cmd/seed
```

seed 会通过正式 Catalog application service 创建支付路由 Collection、动态管理模型、SDK Subscription 和带人工复核的发布模板；重复执行不会新增版本或重复资源。它不会绕过发布流程直接写入 ConfigurationRecord。

## 初步路线图

1. 冻结 Kitex/Protobuf 与 Admin BFF 契约
2. 实现领域内核、MySQL/GORM adapter 和 Goose migrations
3. 实现不可变快照 Config Server 与 Go SDK
4. 实现 QueryPage、ReleaseOrder、权限和审计
5. 提供管理控制台、可观测性、部署和端到端测试
