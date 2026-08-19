import { Alert, Button, Card, Flex, Form, Input, InputNumber, Modal, Select, Space, Switch, Table, Tabs, Tag, Typography, message } from "antd";
import { useCallback, useEffect, useState } from "react";

import { catalogApi } from "./api";
import type { CatalogApi, ModelInput, ModelMetadata, ModelPreview, ReleaseTemplateInput, ReleaseTemplateMetadata } from "./types";

type ModelForm = Omit<ModelInput, "definition"> & { definitionText: string };
type TemplateForm = Omit<ReleaseTemplateInput, "document"> & { documentText: string };

export function ModelTemplatePage({ api = catalogApi, initialTab = "models" }: { api?: CatalogApi; initialTab?: "models" | "templates" }) {
	const [models, setModels] = useState<ModelMetadata[]>([]);
	const [templates, setTemplates] = useState<ReleaseTemplateMetadata[]>([]);
	const [loading, setLoading] = useState(true);
	const [error, setError] = useState<string>();
	const [preview, setPreview] = useState<ModelPreview>();
	const [modelTarget, setModelTarget] = useState<ModelMetadata | "new">();
	const [templateOpen, setTemplateOpen] = useState(false);
	const [modelForm] = Form.useForm<ModelForm>();
	const [templateForm] = Form.useForm<TemplateForm>();

	const load = useCallback(async () => {
		setLoading(true);
		try {
			const [modelPage, templatePage] = await Promise.all([api.listModels({}, 1, 20), api.listTemplates({}, 1, 20)]);
			setModels(modelPage.models);
			setTemplates(templatePage.templates);
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : "加载模型与模板失败");
		} finally {
			setLoading(false);
		}
	}, [api]);
	useEffect(() => { void load(); }, [load]);

	const openModel = (target: ModelMetadata | "new") => {
		setModelTarget(target);
		setPreview(undefined);
		setError(undefined);
		modelForm.setFieldsValue(target === "new" ? { code: "", name: "", collection: "", definitionText: modelExample, enabled: true } : { ...target, definitionText: JSON.stringify(target.definition, null, 2) });
	};

	const modelInput = async (): Promise<ModelInput> => {
		const values = await modelForm.validateFields();
		let definition: Record<string, unknown>;
		try { definition = JSON.parse(values.definitionText) as Record<string, unknown>; }
		catch { throw new Error("Definition 必须是有效的 JSON 对象"); }
		return { code: values.code, name: values.name, collection: values.collection, definition, enabled: values.enabled };
	};

	const runPreview = async () => {
		try {
			const result = await api.previewModel(await modelInput());
			setPreview(result);
			if (result.valid && result.normalizedDefinition) modelForm.setFieldValue("definitionText", JSON.stringify(result.normalizedDefinition, null, 2));
		} catch (cause) { setError(cause instanceof Error ? cause.message : "预检失败"); }
	};

	const saveModel = async () => {
		try {
			const input = await modelInput();
			const result = await api.previewModel(input);
			setPreview(result);
			if (!result.valid) return;
			const normalized = result.normalizedDefinition ? { ...input, definition: result.normalizedDefinition } : input;
			if (modelTarget === "new") await api.createModel(normalized);
			else if (modelTarget) await api.updateModel(modelTarget.code, modelTarget.configRevision, normalized);
			setModelTarget(undefined);
			await load();
			message.success("Model 已保存");
		} catch (cause) { setError(cause instanceof Error ? cause.message : "保存 Model 失败"); }
	};

	const openTemplate = () => {
		setTemplateOpen(true);
		setError(undefined);
		templateForm.setFieldsValue({ code: "", name: "", modelCode: "", releaseTypeCode: "", finalEffect: "BASE_FINAL", schedulingAllowed: false, maxScheduleWindowSeconds: 0, documentText: templateExample, allowedRoles: ["RELEASE_CREATOR", "RELEASE_OPERATOR"], enabled: true });
	};
	const saveTemplate = async () => {
		try {
			const values = await templateForm.validateFields();
			let document: Record<string, unknown>;
			try { document = JSON.parse(values.documentText) as Record<string, unknown>; }
			catch { throw new Error("Template document 必须是有效的 JSON 对象"); }
			await api.createTemplate({ code: values.code, name: values.name, modelCode: values.modelCode, releaseTypeCode: values.releaseTypeCode, finalEffect: values.finalEffect, schedulingAllowed: values.schedulingAllowed, maxScheduleWindowSeconds: values.maxScheduleWindowSeconds, allowedRoles: values.allowedRoles, enabled: values.enabled, document });
			setTemplateOpen(false);
			await load();
			message.success("新的 Template version 已创建");
		} catch (cause) { setError(cause instanceof Error ? cause.message : "创建 Template 失败"); }
	};

	return <Space orientation="vertical" size={16} className="operation-page">
		<Flex justify="space-between" align="center"><div><Typography.Title level={1}>模型与发布模板</Typography.Title><Typography.Paragraph type="secondary">Model 保存前必须通过预检；Template 每次修改都会创建不可变的新版本。</Typography.Paragraph></div><Button loading={loading} onClick={() => void load()}>刷新</Button></Flex>
		{error ? <Alert type="error" showIcon title={error} description="编辑内容已保留，可修正后重试。" /> : null}
		<Card><Tabs defaultActiveKey={initialTab} items={[
			{ key: "models", label: "配置模型", children: <Space orientation="vertical" className="operation-full-width"><Button type="primary" onClick={() => openModel("new")}>新建 Model</Button><Table rowKey="code" loading={loading} dataSource={models} pagination={false} columns={[{ title: "Code", dataIndex: "code", render: (value: string) => <Typography.Text code>{value}</Typography.Text> }, { title: "名称", dataIndex: "name" }, { title: "Collection", dataIndex: "collection" }, { title: "状态", dataIndex: "enabled", render: (value: boolean) => <Tag color={value ? "green" : "default"}>{value ? "启用" : "禁用"}</Tag> }, { title: "Revision", dataIndex: "configRevision" }, { title: "操作", render: (_: unknown, record: ModelMetadata) => <Button aria-label={`编辑 Model ${record.code}`} onClick={() => openModel(record)}>编辑与预检</Button> }]} /></Space> },
			{ key: "templates", label: "发布模板", children: <Space orientation="vertical" className="operation-full-width"><Button type="primary" onClick={openTemplate}>创建 Template version</Button><Table rowKey={(record) => `${record.code}:${record.version}`} loading={loading} dataSource={templates} pagination={false} columns={[{ title: "Code", dataIndex: "code" }, { title: "Version", dataIndex: "version" }, { title: "Model", dataIndex: "modelCode" }, { title: "发布类型", dataIndex: "releaseTypeCode" }, { title: "最终效果", dataIndex: "finalEffect" }, { title: "状态", dataIndex: "enabled", render: (value: boolean) => value ? <Tag color="green">ACTIVE</Tag> : <Tag>HISTORY</Tag> }]} /></Space> },
		]} /></Card>
		<Modal title={modelTarget === "new" ? "新建 Model" : "编辑 Model"} open={modelTarget !== undefined} onCancel={() => setModelTarget(undefined)} footer={[<Button key="preview" onClick={() => void runPreview()}>运行预检</Button>, <Button key="save" type="primary" onClick={() => void saveModel()}>预检并保存</Button>]} width={900} destroyOnHidden><Form form={modelForm} layout="vertical"><Flex gap={12}><Form.Item name="code" label="Code" rules={[{ required: true }]}><Input disabled={modelTarget !== "new"} /></Form.Item><Form.Item name="name" label="名称" rules={[{ required: true }]}><Input /></Form.Item><Form.Item name="collection" label="Collection" rules={[{ required: true }]}><Input /></Form.Item><Form.Item name="enabled" label="启用" valuePropName="checked"><Switch /></Form.Item></Flex><Form.Item name="definitionText" label="Model definition" rules={[{ required: true }]}><Input.TextArea rows={18} className="operation-code-editor" /></Form.Item></Form>{preview ? <Alert type={preview.valid ? "success" : "error"} showIcon title={preview.valid ? "预检通过" : "预检未通过"} description={preview.issues.map((issue) => `${issue.path}: ${issue.message}`).join("；")} /> : null}</Modal>
		<Modal title="创建不可变 Template version" open={templateOpen} onCancel={() => setTemplateOpen(false)} onOk={() => void saveTemplate()} okText="创建版本" width={900} destroyOnHidden><Form form={templateForm} layout="vertical"><Flex gap={12} wrap><Form.Item name="code" label="Code" rules={[{ required: true }]}><Input /></Form.Item><Form.Item name="name" label="名称" rules={[{ required: true }]}><Input /></Form.Item><Form.Item name="modelCode" label="Model code" rules={[{ required: true }]}><Input /></Form.Item><Form.Item name="releaseTypeCode" label="发布类型 code" rules={[{ required: true }]}><Input /></Form.Item><Form.Item name="finalEffect" label="最终效果"><Select options={[{ value: "BASE_FINAL", label: "基础配置最终生效" }, { value: "OVERLAY_FINAL", label: "范围覆盖最终生效" }]} /></Form.Item><Form.Item name="enabled" label="设为活动版本" valuePropName="checked"><Switch /></Form.Item></Flex><Flex gap={12}><Form.Item name="schedulingAllowed" label="允许调度" valuePropName="checked"><Switch /></Form.Item><Form.Item name="maxScheduleWindowSeconds" label="最大调度窗口（秒）"><InputNumber min={0} /></Form.Item><Form.Item name="allowedRoles" label="允许角色"><Select mode="tags" style={{ minWidth: 280 }} /></Form.Item></Flex><Form.Item name="documentText" label="Template document" rules={[{ required: true }]}><Input.TextArea rows={14} className="operation-code-editor" /></Form.Item></Form></Modal>
	</Space>;
}

const modelExample = JSON.stringify({ fields: [], projectionFields: [], keyFields: [], defaultPageSize: 20, maxPageSize: 100, releaseTypes: [], autoFillRules: [] }, null, 2);
const templateExample = JSON.stringify({ steps: [{ code: "apply", type: "BASE_APPLY", requiredRoles: ["RELEASE_OPERATOR"], params: { cleanupScopeOverlay: true } }, { code: "complete", type: "COMPLETE", requiredRoles: [], params: {} }] }, null, 2);
