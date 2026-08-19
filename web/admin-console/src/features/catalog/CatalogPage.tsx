import { Alert, Button, Card, Checkbox, Flex, Form, Input, InputNumber, Modal, Select, Space, Switch, Table, Tabs, Tag, Typography, message } from "antd";
import { useCallback, useEffect, useState } from "react";

import { catalogApi } from "./api";
import type { CatalogApi, CollectionInput, CollectionMetadata, SubscriptionInput, SubscriptionMetadata } from "./types";

const pageSize = 20;
const fieldTypes = ["STRING", "INT64", "FLOAT64", "BOOL", "TIMESTAMP", "JSON"].map((value) => ({ value, label: value }));

export function CatalogPage({ api = catalogApi, initialTab = "collections" }: { api?: CatalogApi; initialTab?: "collections" | "subscriptions" }) {
	const [collections, setCollections] = useState<CollectionMetadata[]>([]);
	const [subscriptions, setSubscriptions] = useState<SubscriptionMetadata[]>([]);
	const [loading, setLoading] = useState(true);
	const [error, setError] = useState<string>();
	const [collectionTarget, setCollectionTarget] = useState<CollectionMetadata | "new">();
	const [subscriptionTarget, setSubscriptionTarget] = useState<SubscriptionMetadata | "new">();
	const [collectionForm] = Form.useForm<CollectionInput>();
	const [subscriptionForm] = Form.useForm<SubscriptionInput>();

	const load = useCallback(async () => {
		setLoading(true);
		setError(undefined);
		try {
			const [collectionPage, subscriptionPage] = await Promise.all([
				api.listCollections(1, pageSize),
				api.listSubscriptions({}, 1, pageSize),
			]);
			setCollections(collectionPage.collections);
			setSubscriptions(subscriptionPage.subscriptions);
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : "加载元数据失败");
		} finally {
			setLoading(false);
		}
	}, [api]);

	useEffect(() => { void load(); }, [load]);

	const openCollection = (target: CollectionMetadata | "new") => {
		setError(undefined);
		setCollectionTarget(target);
		collectionForm.setFieldsValue(target === "new" ? {
			name: "", description: "", fields: [{ name: "", displayName: "", type: "STRING", required: true, sensitive: false, description: "", displayOrder: 0, validationRules: [] }],
			keyFields: [], sdkDeliveryEnabled: false, schemaVersion: 1, status: "ENABLED",
		} : target);
	};

	const saveCollection = async () => {
		try {
			const input = await collectionForm.validateFields();
			input.fields = input.fields.map((field, index) => ({ ...field, description: field.description ?? "", displayOrder: index, validationRules: field.validationRules ?? [] }));
			if (collectionTarget === "new") await api.createCollection(input);
			else if (collectionTarget) await api.updateCollection(collectionTarget.name, collectionTarget.configRevision, input);
			setCollectionTarget(undefined);
			await load();
			message.success("Collection 已保存");
		} catch (cause) {
			if (cause instanceof Error) setError(cause.message);
		}
	};

	const openSubscription = (target: SubscriptionMetadata | "new") => {
		setError(undefined);
		setSubscriptionTarget(target);
		subscriptionForm.setFieldsValue(target === "new" ? { consumerId: "", collection: "", indexName: "", indexFields: [], cardinality: "ONE_TO_ONE", enabled: true } : target);
	};

	const saveSubscription = async () => {
		try {
			const input = await subscriptionForm.validateFields();
			if (subscriptionTarget === "new") await api.createSubscription(input);
			else if (subscriptionTarget) await api.updateSubscription(subscriptionTarget.id, subscriptionTarget.configRevision, input);
			setSubscriptionTarget(undefined);
			await load();
			message.success("Subscription 已保存");
		} catch (cause) {
			if (cause instanceof Error) setError(cause.message);
		}
	};

	return (
		<Space orientation="vertical" size={16} className="operation-page">
			<Flex justify="space-between" align="center">
				<div><Typography.Title level={1}>元数据管理</Typography.Title><Typography.Paragraph type="secondary">管理配置结构与消费者索引。所有修改均使用修订号校验，冲突时不会覆盖他人修改。</Typography.Paragraph></div>
				<Button onClick={() => void load()} loading={loading}>刷新</Button>
			</Flex>
			{error ? <Alert type="error" showIcon title={error} description="当前编辑内容会保留，请刷新数据并核对后重试。" /> : null}
			<Card>
				<Tabs defaultActiveKey={initialTab} items={[
					{ key: "collections", label: "配置集合", children: <Space orientation="vertical" className="operation-full-width">
						<Button type="primary" onClick={() => openCollection("new")}>新建 Collection</Button>
						<Table rowKey="name" loading={loading} dataSource={collections} pagination={false} columns={[
							{ title: "名称", dataIndex: "name", render: (value: string) => <Typography.Text code>{value}</Typography.Text> },
							{ title: "说明", dataIndex: "description" },
							{ title: "状态", dataIndex: "status", render: (value: string) => <Tag color={value === "ENABLED" ? "green" : "default"}>{value}</Tag> },
							{ title: "SDK 分发", dataIndex: "sdkDeliveryEnabled", render: (value: boolean) => value ? "已开启" : "未开启" },
							{ title: "Schema", dataIndex: "schemaVersion" }, { title: "Revision", dataIndex: "configRevision" },
							{ title: "操作", render: (_: unknown, record: CollectionMetadata) => <Button aria-label={`编辑 Collection ${record.name}`} onClick={() => openCollection(record)}>编辑</Button> },
						]} />
					</Space> },
					{ key: "subscriptions", label: "消费者与订阅", children: <Space orientation="vertical" className="operation-full-width">
						<Button type="primary" onClick={() => openSubscription("new")}>新建 Subscription</Button>
						<Table rowKey="id" loading={loading} dataSource={subscriptions} pagination={false} columns={[
							{ title: "Consumer", dataIndex: "consumerId" }, { title: "Collection", dataIndex: "collection" }, { title: "索引名", dataIndex: "indexName" },
							{ title: "索引字段", dataIndex: "indexFields", render: (values: string[]) => values.join(", ") },
							{ title: "基数", dataIndex: "cardinality" }, { title: "状态", dataIndex: "enabled", render: (value: boolean) => <Tag color={value ? "green" : "default"}>{value ? "启用" : "禁用"}</Tag> },
							{ title: "Revision", dataIndex: "configRevision" }, { title: "操作", render: (_: unknown, record: SubscriptionMetadata) => <Button aria-label={`编辑 Subscription ${record.indexName}`} onClick={() => openSubscription(record)}>编辑</Button> },
						]} />
					</Space> },
				]} />
			</Card>
			<Modal title={collectionTarget === "new" ? "新建 Collection" : "编辑 Collection"} open={collectionTarget !== undefined} onCancel={() => setCollectionTarget(undefined)} onOk={() => void saveCollection()} okText="保存" width={1000} destroyOnHidden>
				<Form form={collectionForm} layout="vertical">
					<Flex gap={12}><Form.Item name="name" label="名称" rules={[{ required: true }]}><Input disabled={collectionTarget !== "new"} /></Form.Item><Form.Item name="description" label="说明"><Input /></Form.Item><Form.Item name="schemaVersion" label="Schema 版本" rules={[{ required: true }]}><InputNumber min={1} /></Form.Item><Form.Item name="status" label="状态"><Select options={[{ value: "ENABLED", label: "启用" }, { value: "DISABLED", label: "禁用" }]} /></Form.Item><Form.Item name="sdkDeliveryEnabled" label="允许 SDK 分发" valuePropName="checked"><Switch /></Form.Item></Flex>
					<Form.List name="fields">{(fields, { add, remove }) => <Space orientation="vertical" className="operation-full-width">
						<Typography.Title level={5}>字段</Typography.Title>
						{fields.map((field, index) => <Flex key={field.key} gap={8} align="center" wrap>
							<Form.Item {...field} name={[field.name, "name"]} label={index === 0 ? "字段名" : undefined} rules={[{ required: true }]}><Input /></Form.Item>
							<Form.Item {...field} name={[field.name, "displayName"]} label={index === 0 ? "显示名" : undefined} rules={[{ required: true }]}><Input /></Form.Item>
							<Form.Item {...field} name={[field.name, "type"]} label={index === 0 ? "类型" : undefined}><Select options={fieldTypes} style={{ width: 130 }} /></Form.Item>
							<Form.Item {...field} name={[field.name, "required"]} valuePropName="checked"><Checkbox>必填</Checkbox></Form.Item>
							<Form.Item {...field} name={[field.name, "sensitive"]} valuePropName="checked"><Checkbox>敏感</Checkbox></Form.Item>
							<Form.Item {...field} name={[field.name, "displayOrder"]} hidden><InputNumber /></Form.Item><Form.Item {...field} name={[field.name, "description"]} hidden><Input /></Form.Item><Form.Item {...field} name={[field.name, "validationRules"]} hidden><Input /></Form.Item>
							<Button danger disabled={fields.length === 1} onClick={() => remove(field.name)}>移除</Button>
						</Flex>)}
						<Button onClick={() => add({ name: "", displayName: "", type: "STRING", required: false, sensitive: false, description: "", displayOrder: fields.length, validationRules: [] })}>添加字段</Button>
					</Space>}</Form.List>
					<Form.Item name="keyFields" label="主键字段" rules={[{ required: true }]}><Select mode="tags" placeholder="输入字段名后回车" /></Form.Item>
				</Form>
			</Modal>
			<Modal title={subscriptionTarget === "new" ? "新建 Subscription" : "编辑 Subscription"} open={subscriptionTarget !== undefined} onCancel={() => setSubscriptionTarget(undefined)} onOk={() => void saveSubscription()} okText="保存" destroyOnHidden>
				<Form form={subscriptionForm} layout="vertical"><Form.Item name="consumerId" label="Consumer ID" rules={[{ required: true }]}><Input /></Form.Item><Form.Item name="collection" label="Collection" rules={[{ required: true }]}><Select options={collections.map((item) => ({ value: item.name, label: item.name }))} /></Form.Item><Form.Item name="indexName" label="索引名" rules={[{ required: true }]}><Input /></Form.Item><Form.Item name="indexFields" label="索引字段" rules={[{ required: true }]}><Select mode="tags" /></Form.Item><Form.Item name="cardinality" label="基数"><Select options={[{ value: "ONE_TO_ONE", label: "一对一" }, { value: "ONE_TO_MANY", label: "一对多" }]} /></Form.Item><Form.Item name="enabled" label="启用" valuePropName="checked"><Switch /></Form.Item></Form>
			</Modal>
		</Space>
	);
}
