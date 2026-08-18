# 金融配置中心

> 像操作 MySQL 表一样操作金融业务配置。

`financial_configuration_center` 面向金融业务场景，目标是用熟悉的表、字段、行和 SQL 语义管理配置，同时提供配置中心需要的版本、发布、回滚、审计和权限能力。

## 核心理念

传统配置中心通常以 `key-value` 或配置文件为核心。金融业务配置往往天然具有结构，例如费率、限额、路由、产品参数和风控规则。项目将这些配置抽象为表：

- 一类配置对应一张表
- 配置项通过字段定义类型和约束
- 一条业务配置对应一行记录
- 查询和变更采用接近 SQL 的操作方式
- 每次变更都保留版本、审批和审计记录

## 期望体验

```sql
CREATE TABLE payment_route (
  channel       VARCHAR(32) PRIMARY KEY,
  enabled       BOOLEAN NOT NULL,
  priority      INT NOT NULL,
  daily_limit   DECIMAL(18, 2),
  updated_by    VARCHAR(64)
);

SELECT *
FROM payment_route
WHERE enabled = true
ORDER BY priority;

UPDATE payment_route
SET daily_limit = 1000000
WHERE channel = 'bank_transfer';
```

这些操作最终对应配置的校验、发布和版本记录，而不是直接修改业务数据库。

## 目标能力

- 表结构与字段类型管理
- SQL 风格的配置查询和变更
- 多环境与多租户隔离
- 草稿、发布、回滚和变更对比
- 完整审计日志
- RBAC 权限与审批流程
- SDK、HTTP API 和变更订阅
- 高可用读取与本地缓存

## 典型场景

- 支付渠道与路由配置
- 产品参数和费率配置
- 交易限额配置
- 风控规则参数
- 机构、币种和地区配置
- 营销活动与权益配置

## 项目状态

项目处于设计阶段。接下来将优先定义领域模型、SQL 子集、存储架构和最小可用版本。

## 初步路线图

1. 定义配置表、字段、记录和版本模型
2. 实现表结构管理与基础 CRUD API
3. 支持 SQL 风格查询与变更
4. 增加发布、回滚和审计能力
5. 提供管理控制台与客户端 SDK
