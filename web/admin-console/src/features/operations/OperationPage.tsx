import {
  Alert,
  Button,
  Card,
  Checkbox,
  Descriptions,
  Drawer,
  Flex,
  Form,
  Input,
  InputNumber,
  Modal,
  Select,
  Space,
  Spin,
  Steps,
  Table,
  Tag,
  Typography,
  message,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import { useCallback, useEffect, useMemo, useState } from "react";

import { operationApi } from "./api";
import type {
  CreateReleaseRequest,
  InteractionField,
  OperationApi,
  PageResult,
  ReleaseAction,
  ReleaseDetail,
} from "./types";

const scope = { region: "cn", environment: "production" } as const;
const modelCode = "payment-route-admin";

interface DraftRow {
  id: string;
  after: Record<string, string>;
}

export function OperationPage({ api = operationApi }: { api?: OperationApi }) {
  const [form] = Form.useForm<Record<string, unknown>>();
  const [page, setPage] = useState<PageResult>();
  const [drafts, setDrafts] = useState<DraftRow[]>([]);
  const [release, setRelease] = useState<ReleaseDetail>();
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [addOpen, setAddOpen] = useState(false);
  const [reviewOpen, setReviewOpen] = useState(false);
  const [releaseTypeCode, setReleaseTypeCode] = useState<string>();
  const [description, setDescription] = useState("");
  const [comment, setComment] = useState("");
  const [error, setError] = useState<string>();

  const load = useCallback(async () => {
    setLoading(true);
    setError(undefined);
    try {
      const loaded = await api.queryPage({ modelCode, scope, queryType: "ALL" });
      setPage(loaded);
      setReleaseTypeCode((current) =>
        loaded.releaseTypes.some((releaseType) => releaseType.available && releaseType.code === current)
          ? current
          : loaded.releaseTypes.find((releaseType) => releaseType.available)?.code,
      );
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "加载配置失败");
    } finally {
      setLoading(false);
    }
  }, [api]);

  useEffect(() => {
    void load();
  }, [load]);

  const projectedFields = useMemo(
    () =>
      page?.projectionFields
        .map((name) => page.interactionFields.find((field) => field.name === name))
        .filter((field): field is InteractionField => field !== undefined) ?? [],
    [page],
  );
  const columns: ColumnsType<PageResult["rows"][number]> = projectedFields.map((field) => ({
    title: field.displayName,
    dataIndex: ["values", field.name],
    key: field.name,
    render: (value: string | undefined) => renderValue(field, value),
  }));
  const selectedReleaseType = page?.releaseTypes.find((releaseType) => releaseType.code === releaseTypeCode);

  const openAdd = () => {
    const defaults = Object.fromEntries(
      (page?.interactionFields ?? [])
        .filter((field) => field.editable && field.defaultValue !== undefined)
        .map((field) => [field.name, fromCanonicalDefault(field)]),
    );
    form.setFieldsValue(defaults);
    setAddOpen(true);
  };

  const addDraft = async () => {
    const values = await form.validateFields();
    if (!page) return;
    const after = Object.fromEntries(
      page.interactionFields
        .filter((field) => field.editable && values[field.name] !== undefined)
        .map((field) => [field.name, toCanonicalInput(field, values[field.name])]),
    );
    setDrafts((current) => [...current, { id: crypto.randomUUID(), after }]);
    setAddOpen(false);
    form.resetFields();
  };

  const createRelease = async () => {
    if (!page || drafts.length === 0 || !selectedReleaseType?.available) return;
    setSubmitting(true);
    try {
      const request: CreateReleaseRequest = {
        modelCode: page.modelCode,
        releaseTypeCode: selectedReleaseType.code,
        description: description.trim() || `Add ${drafts.length} configuration record(s)`,
        scope,
        items: drafts.map((draft) => ({
          action: "ADD",
          after: draft.after,
          expectedRecordRevision: 0,
          expectedCollectionRevision: page.collectionRevision,
        })),
      };
      setRelease(await api.createRelease(request, crypto.randomUUID()));
      message.success("发布单已创建，配置尚未生效");
    } catch (cause) {
      message.error(cause instanceof Error ? cause.message : "创建发布单失败");
    } finally {
      setSubmitting(false);
    }
  };

  const act = async (action: ReleaseAction) => {
    if (!release || !release.allowedActions.includes(action)) return;
    if (action === "REJECT" && comment.trim() === "") {
      message.warning("驳回时必须填写原因");
      return;
    }
    setSubmitting(true);
    try {
      const next = await api.actOnRelease(release.order.id, crypto.randomUUID(), {
        action,
        expectedOrderRevision: release.order.entityRevision,
        expectedCurrentStep: release.order.currentStep,
        ...(comment.trim() ? { comment: comment.trim() } : {}),
      });
      setRelease(next);
      setComment("");
      if (next.order.status === "SUCCEEDED") {
        setDrafts([]);
        setReviewOpen(false);
        await load();
        message.success("发布完成，页面已刷新");
      }
    } catch (cause) {
      message.error(cause instanceof Error ? cause.message : "发布动作失败");
    } finally {
      setSubmitting(false);
    }
  };

  if (loading && !page) {
    return <Spin fullscreen description="加载配置模型" />;
  }

  return (
    <Space orientation="vertical" size={16} className="operation-page">
      <Flex justify="space-between" align="start" gap={16}>
        <div>
          <Typography.Title level={1}>统一操作入口</Typography.Title>
          <Space wrap>
            <Tag color="blue">Region: {scope.region}</Tag>
            <Tag color="green">Environment: {scope.environment}</Tag>
            <Tag>Model: {page?.modelName ?? modelCode}</Tag>
          </Space>
        </div>
        <Space>
          <Button onClick={() => void load()}>刷新</Button>
          <Button type="primary" onClick={openAdd} disabled={!page}>
            新增配置
          </Button>
          <Button onClick={() => setReviewOpen(true)} disabled={drafts.length === 0}>
            审阅并发布
          </Button>
        </Space>
      </Flex>

      {error ? <Alert type="error" title="加载失败" description={error} showIcon /> : null}
      <Card
        title={page?.modelName ?? "配置"}
        extra={
          <Space>
            <Tag color={drafts.length > 0 ? "orange" : "default"}>草稿 {drafts.length}</Tag>
            <Typography.Text type="secondary">Collection revision {page?.collectionRevision ?? "—"}</Typography.Text>
          </Space>
        }
      >
        <Table
          rowKey="recordKey"
          dataSource={page?.rows ?? []}
          columns={columns}
          pagination={{ pageSize: page?.page.size ?? 20, total: page?.page.totalNumber ?? 0 }}
          locale={{ emptyText: "当前 Environment 尚无配置" }}
        />
      </Card>

      <Modal title="新增配置" open={addOpen} onCancel={() => setAddOpen(false)} onOk={() => void addDraft()} okText="加入草稿" destroyOnHidden>
        <Form form={form} layout="vertical" preserve={false}>
          {page?.interactionFields.filter((field) => field.editable).map((field) => (
            <Form.Item
              key={field.name}
              name={field.name}
              label={field.displayName}
              extra={field.description}
              valuePropName={field.uiControl === "BOOLEAN" ? "checked" : "value"}
              rules={[{ required: field.required, message: `${field.displayName} 为必填项` }]}
            >
              {editor(field)}
            </Form.Item>
          ))}
        </Form>
      </Modal>

      <Drawer title="审阅差异与发布" size="large" open={reviewOpen} onClose={() => setReviewOpen(false)}>
        <Space orientation="vertical" size={16} className="operation-review">
          <Alert type="info" showIcon title="所有修改仍在浏览器草稿中；创建发布单不会立即改写配置。" />
          {drafts.map((draft, index) => (
            <Card key={draft.id} size="small" title={`ADD #${index + 1}`}>
              <Descriptions column={1} size="small">
                {projectedFields.map((field) => (
                  <Descriptions.Item key={field.name} label={field.displayName}>
                    {renderValue(field, draft.after[field.name])}
                  </Descriptions.Item>
                ))}
              </Descriptions>
            </Card>
          ))}
          {!release ? (
            <Card title="发布信息" size="small">
              <Space orientation="vertical" size={12} className="operation-full-width">
                <label htmlFor="release-type">发布方式</label>
                <Select
                  id="release-type"
                  aria-label="发布方式"
                  value={releaseTypeCode}
                  onChange={setReleaseTypeCode}
                  placeholder="请选择发布方式"
                  options={(page?.releaseTypes ?? []).map((releaseType) => ({
                    value: releaseType.code,
                    label: `${releaseType.name} · ${releaseType.templateCode}${releaseType.available ? "" : `（不可用：${releaseType.unavailableReasonCode ?? "UNKNOWN"}）`}`,
                    disabled: !releaseType.available,
                  }))}
                />
                <label htmlFor="release-description">发布说明</label>
                <Input.TextArea
                  id="release-description"
                  aria-label="发布说明"
                  value={description}
                  onChange={(event) => setDescription(event.target.value)}
                  placeholder="说明本次变更的目的与影响"
                  autoSize={{ minRows: 2, maxRows: 4 }}
                />
                {selectedReleaseType ? (
                  <Typography.Text type="secondary">将使用模板 {selectedReleaseType.templateCode}</Typography.Text>
                ) : (
                  <Alert type="warning" showIcon title="当前模型没有可用的发布方式" />
                )}
                <Button type="primary" loading={submitting} disabled={!selectedReleaseType?.available} onClick={() => void createRelease()}>
                  创建发布单
                </Button>
              </Space>
            </Card>
          ) : (
            <Card title="发布进度">
              <Descriptions column={1} size="small">
                <Descriptions.Item label="Release ID">{release.order.id}</Descriptions.Item>
                <Descriptions.Item label="状态">{release.order.status}</Descriptions.Item>
                <Descriptions.Item label="当前步骤">{release.order.currentStep} · {release.order.currentStepType}</Descriptions.Item>
                <Descriptions.Item label="步骤状态">{release.order.currentStepStatus}</Descriptions.Item>
                <Descriptions.Item label="Entity revision">{release.order.entityRevision}</Descriptions.Item>
              </Descriptions>
              <Steps
                responsive
                current={Math.max(0, release.steps.findIndex((step) => step.code === release.order.currentStep))}
                items={release.steps.map((step) => ({
                  title: stepLabel(step.type),
                  content: `${step.code} · ${step.status}`,
                  status: stepVisualStatus(step.status, step.code === release.order.currentStep),
                }))}
              />
              {release.allowedActions.some((action) => action === "APPROVE" || action === "REJECT") ? (
                <Space orientation="vertical" className="operation-full-width">
                  <label htmlFor="approval-comment">审批意见</label>
                  <Input.TextArea
                    id="approval-comment"
                    aria-label="审批意见"
                    value={comment}
                    onChange={(event) => setComment(event.target.value)}
                    placeholder="批准可选；驳回必填"
                    autoSize={{ minRows: 2, maxRows: 4 }}
                  />
                </Space>
              ) : null}
              <Space wrap>
                {release.allowedActions.map((action) => (
                  <Button
                    key={action}
                    type={action === "REJECT" ? "default" : "primary"}
                    danger={action === "REJECT"}
                    loading={submitting}
                    disabled={submitting}
                    onClick={() => void act(action)}
                  >
                    {actionLabel(action, release)}
                  </Button>
                ))}
              </Space>
            </Card>
          )}
        </Space>
      </Drawer>
    </Space>
  );
}

function editor(field: InteractionField) {
  switch (field.uiControl) {
    case "NUMBER":
      return <InputNumber stringMode className="operation-full-width" />;
    case "BOOLEAN":
      return <Checkbox>启用</Checkbox>;
    case "SELECT":
      return <Select options={field.options.map((option) => ({ value: option.code, label: option.label, disabled: option.disabled }))} />;
    case "TEXTAREA":
    case "JSON":
      return <Input.TextArea autoSize={{ minRows: 3, maxRows: 8 }} />;
    default:
      return <Input />;
  }
}

function renderValue(field: InteractionField, value?: string) {
  if (field.sensitive && value !== undefined) return <Tag>••••••</Tag>;
  if (field.type === "BOOL") return <Tag color={value === "true" ? "green" : "default"}>{value === "true" ? "是" : "否"}</Tag>;
  return value ?? "—";
}

function fromCanonicalDefault(field: InteractionField): unknown {
  if (field.type === "BOOL") return field.defaultValue === "true";
  return field.defaultValue;
}

function toCanonicalInput(field: InteractionField, value: unknown): string {
  if (field.type === "BOOL") return value ? "true" : "false";
  return String(value);
}

function actionLabel(action: ReleaseAction, release: ReleaseDetail) {
  if (action === "APPROVE") return "批准";
  if (action === "REJECT") return "驳回";
  if (action === "ADVANCE") return "推进下一步";
  if (release.order.currentStepType === "MANUAL_REVIEW") return "提交人工审批";
  if (release.order.currentStepType === "COMPLETE") return "完成发布";
  return `执行 ${release.order.currentStepType}`;
}

function stepLabel(type: string) {
  switch (type) {
    case "MANUAL_REVIEW":
      return "人工复核";
    case "BASE_APPLY":
      return "基础生效";
    case "COMPARE":
      return "对比验证";
    case "COMPLETE":
      return "完成";
    default:
      return type;
  }
}

function stepVisualStatus(status: string, current: boolean): "wait" | "process" | "finish" | "error" {
  if (status === "REJECTED") return "error";
  if (status === "EXECUTED" || status === "APPROVED") return "finish";
  if (current && (status === "PENDING" || status === "EXECUTING")) return "process";
  return "wait";
}
