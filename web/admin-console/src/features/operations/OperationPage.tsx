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
  Progress,
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
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { operationApi } from "./api";
import type {
  CreateReleaseRequest,
	FilterOperator,
  InteractionField,
  OperationApi,
  PageResult,
  PageRow,
	QueryCondition,
  ReleaseAction,
  ReleaseDetail,
  Scope,
} from "./types";

const defaultScope: Scope = { region: "cn", environment: "production" };
const modelCode = "payment-route-admin";

interface DraftRow {
  id: string;
  action: "ADD" | "MODIFY";
  baseBefore?: Record<string, string>;
  effectiveBefore?: Record<string, string>;
  after: Record<string, string>;
  expectedRecordRevision: number;
  preserveSensitiveFields?: string[];
}

interface FilterDraft {
	operator: FilterOperator;
	value?: string;
	lower?: string;
	upper?: string;
}

interface RevealTarget {
  row: PageRow;
  field: InteractionField;
}

export function OperationPage({ api = operationApi }: { api?: OperationApi }) {
  const [form] = Form.useForm<Record<string, unknown>>();
  const [activeScope, setActiveScope] = useState<Scope>(defaultScope);
  const [scopeDraft, setScopeDraft] = useState<Scope>(defaultScope);
  const [activePreviewBucket, setActivePreviewBucket] = useState<number>();
  const [previewBucketDraft, setPreviewBucketDraft] = useState<number>();
  const [page, setPage] = useState<PageResult>();
  const [drafts, setDrafts] = useState<DraftRow[]>([]);
  const [release, setRelease] = useState<ReleaseDetail>();
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [addOpen, setAddOpen] = useState(false);
  const [editingRow, setEditingRow] = useState<PageRow>();
  const [reviewOpen, setReviewOpen] = useState(false);
  const [releaseTypeCode, setReleaseTypeCode] = useState<string>();
  const [description, setDescription] = useState("");
  const [comment, setComment] = useState("");
  const [replaceSensitive, setReplaceSensitive] = useState<Record<string, boolean>>({});
	const [filterDrafts, setFilterDrafts] = useState<Record<string, FilterDraft>>({});
	const [activeConditions, setActiveConditions] = useState<QueryCondition[]>([]);
  const [revealTarget, setRevealTarget] = useState<RevealTarget>();
  const [revealReason, setRevealReason] = useState("");
  const [revealing, setRevealing] = useState(false);
  const [revealedValues, setRevealedValues] = useState<Record<string, string>>({});
  const revealTimers = useRef(new Map<string, ReturnType<typeof setTimeout>>());
  const revealSequence = useRef(0);
  const [error, setError] = useState<string>();

  const clearRevealed = useCallback(() => {
    revealSequence.current += 1;
    for (const timer of revealTimers.current.values()) clearTimeout(timer);
    revealTimers.current.clear();
    setRevealedValues({});
  }, []);

  const load = useCallback(async () => {
    clearRevealed();
    setLoading(true);
    setError(undefined);
    try {
      const loaded = await api.queryPage({
        modelCode,
        scope: activeScope,
        queryType: "ALL",
        ...(activePreviewBucket === undefined ? {} : { previewBucket: activePreviewBucket }),
      });
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
  }, [activePreviewBucket, activeScope, api, clearRevealed]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => () => {
    for (const timer of revealTimers.current.values()) clearTimeout(timer);
  }, []);

  const projectedFields = useMemo(
    () =>
      page?.projectionFields
        .map((name) => page.interactionFields.find((field) => field.name === name))
        .filter((field): field is InteractionField => field !== undefined) ?? [],
    [page],
  );
  const columns: ColumnsType<PageResult["rows"][number]> = [
    ...projectedFields.map((field) => ({
      title: field.displayName,
      dataIndex: ["values", field.name],
      key: field.name,
      render: (value: string | undefined, row: PageRow) => (
        <Space size={4}>
          {field.sensitive && row.maskedFields.includes(field.name) ? (
            revealedValues[revealCellKey(row.recordKey, field.name)] !== undefined ? (
              <>
                <Typography.Text code>{revealedValues[revealCellKey(row.recordKey, field.name)]}</Typography.Text>
                <Button type="link" size="small" aria-label={`隐藏 ${field.displayName} ${row.recordKey}`} onClick={() => hideRevealed(row.recordKey, field.name)}>隐藏</Button>
              </>
            ) : (
              <>
                <Tag>••••••</Tag>
                <Button type="link" size="small" aria-label={`临时查看 ${field.displayName} ${row.recordKey}`} onClick={() => { setRevealTarget({ row, field }); setRevealReason(""); }}>临时查看</Button>
              </>
            )
          ) : renderValue(field, value)}
          {row.changedFields.includes(field.name) ? <Tag color="gold">Scope 覆盖</Tag> : null}
        </Space>
      ),
    })),
    {
      title: "操作",
      key: "actions",
      render: (_value: unknown, row: PageRow) => (
        <Button
          type="link"
          aria-label={`修改 ${row.values[projectedFields[0]?.name ?? ""] ?? row.recordKey}`}
          disabled={!row.basePresent}
          onClick={() => openEdit(row)}
        >
          修改
        </Button>
      ),
    },
  ];
  const selectedReleaseType = page?.releaseTypes.find((releaseType) => releaseType.code === releaseTypeCode);
  const rolloutSteps = release?.steps.filter((step) => step.type === "PERCENT_ROLLOUT") ?? [];
  const rolloutCoverage = coveredBuckets(rolloutSteps.filter((step) => step.status === "EXECUTED" || step.status === "APPROVED"));
  const compareSteps = release?.steps.filter((step) => step.compareResult !== undefined) ?? [];

  const openAdd = () => {
    setEditingRow(undefined);
    setReplaceSensitive({});
    form.resetFields();
    const defaults = Object.fromEntries(
      (page?.interactionFields ?? [])
        .filter((field) => field.editable && field.defaultValue !== undefined)
        .map((field) => [field.name, fromCanonicalDefault(field)]),
    );
    form.setFieldsValue(defaults);
    setAddOpen(true);
  };

  function openEdit(row: PageRow) {
    setEditingRow(row);
    setReplaceSensitive({});
    form.resetFields();
    form.setFieldsValue(Object.fromEntries(
      (page?.interactionFields ?? [])
        .filter((field) => field.editable && row.values[field.name] !== undefined)
        .map((field) => [field.name, fromCanonicalValue(field, row.values[field.name])]),
    ));
    setAddOpen(true);
  }

  const addDraft = async () => {
    const values = await form.validateFields().catch(() => undefined);
    if (!values) return;
    if (!page) return;
    const edited = Object.fromEntries(
      page.interactionFields
        .filter((field) => field.editable && values[field.name] !== undefined && (!editingRow || !field.sensitive || replaceSensitive[field.name]))
        .map((field) => [field.name, toCanonicalInput(field, values[field.name])]),
    );
    const after = editingRow ? { ...editingRow.values, ...edited } : edited;
    const preserveSensitiveFields = editingRow
      ? page.interactionFields
        .filter((field) => field.sensitive && editingRow.maskedFields.includes(field.name) && !replaceSensitive[field.name])
        .map((field) => field.name)
      : [];
    setDrafts((current) => [...current, editingRow ? {
      id: crypto.randomUUID(), action: "MODIFY", baseBefore: { ...editingRow.baseValues },
      effectiveBefore: { ...editingRow.values }, after, expectedRecordRevision: editingRow.recordRevision,
      ...(preserveSensitiveFields.length > 0 ? { preserveSensitiveFields } : {}),
    } : {
      id: crypto.randomUUID(), action: "ADD", after, expectedRecordRevision: 0,
    }]);
    setAddOpen(false);
    setEditingRow(undefined);
    form.resetFields();
  };

  const applyScope = () => {
    const region = scopeDraft.region.trim();
    const environment = scopeDraft.environment.trim();
    const stage = scopeDraft.stage?.trim();
    if (!region || !environment) {
      message.warning("Region 与 Environment 为必填项");
      return;
    }
    setDrafts([]);
    setRelease(undefined);
    setReviewOpen(false);
	clearRevealed();
	setFilterDrafts({});
	setActiveConditions([]);
    setActiveScope({ region, environment, ...(stage ? { stage } : {}) });
    setActivePreviewBucket(previewBucketDraft);
  };

	const queryData = async (conditions: QueryCondition[], pageNumber = 1, pageSize = page?.page.size ?? 20) => {
		if (!page) return false;
		clearRevealed();
		setLoading(true);
		setError(undefined);
		try {
			const loaded = await api.queryPage({
				modelCode,
				scope: activeScope,
				queryType: "ONLY_DATA",
				conditions,
				pageNumber,
				pageSize,
				...(activePreviewBucket === undefined ? {} : { previewBucket: activePreviewBucket }),
			});
			if (!sameSnapshot(page, loaded)) {
				setFilterDrafts({});
				setActiveConditions([]);
				message.info("配置已更新，已回到第一页");
				await load();
				return false;
			}
			setPage({
				...loaded,
				projectionFields: page.projectionFields,
				interactionFields: page.interactionFields,
				releaseTypes: page.releaseTypes,
			});
			return true;
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : "查询失败");
			return false;
		} finally {
			setLoading(false);
		}
	};

  function hideRevealed(recordKey: string, fieldName: string) {
    const key = revealCellKey(recordKey, fieldName);
    const timer = revealTimers.current.get(key);
    if (timer) clearTimeout(timer);
    revealTimers.current.delete(key);
    setRevealedValues((current) => {
      const next = { ...current };
      delete next[key];
      return next;
    });
  }

  const revealSensitive = async () => {
    if (!page || !revealTarget || !revealReason.trim()) return;
    const target = revealTarget;
    const sequence = revealSequence.current;
    setRevealing(true);
    try {
      const revealed = await api.revealSensitive({
        modelCode: page.modelCode,
        scope: activeScope,
        recordKey: target.row.recordKey,
        fieldName: target.field.name,
        expectedRecordRevision: target.row.recordRevision,
        expectedCollectionRevision: page.collectionRevision,
        expectedModelRevision: page.modelRevision,
        expectedServerEpoch: page.snapshot.serverEpoch,
        expectedSnapshotInstance: page.snapshot.snapshotInstance,
        expectedSnapshotGeneration: page.snapshot.snapshotGeneration,
        reason: revealReason.trim(),
        ...(activePreviewBucket === undefined ? {} : { previewBucket: activePreviewBucket }),
      }, crypto.randomUUID());
      if (sequence !== revealSequence.current) return;
      const key = revealCellKey(target.row.recordKey, target.field.name);
      setRevealedValues((current) => ({ ...current, [key]: revealed.value }));
      const expiresAt = Date.parse(revealed.expiresAt);
      const delay = Number.isFinite(expiresAt) ? Math.max(0, Math.min(60_000, expiresAt - Date.now())) : 60_000;
      const previous = revealTimers.current.get(key);
      if (previous) clearTimeout(previous);
      revealTimers.current.set(key, setTimeout(() => hideRevealed(target.row.recordKey, target.field.name), delay));
      setRevealTarget(undefined);
      setRevealReason("");
    } catch (cause) {
      message.error(cause instanceof Error ? cause.message : "临时查看失败");
    } finally {
      setRevealing(false);
    }
  };

	const submitQuery = async () => {
		if (!page) return;
		const conditions = buildConditions(page.interactionFields, filterDrafts);
		if (await queryData(conditions, 1)) setActiveConditions(conditions);
	};

	const resetQuery = async () => {
		setFilterDrafts({});
		if (await queryData([], 1)) setActiveConditions([]);
	};

  const createRelease = async () => {
    if (!page || drafts.length === 0 || !selectedReleaseType?.available) return;
    setSubmitting(true);
    try {
      const request: CreateReleaseRequest = {
        modelCode: page.modelCode,
        releaseTypeCode: selectedReleaseType.code,
        description: description.trim() || `Change ${drafts.length} configuration record(s)`,
        scope: activeScope,
        items: drafts.map((draft) => ({
          action: draft.action,
          ...(draft.baseBefore ? { baseBefore: draft.baseBefore } : {}),
          ...(draft.effectiveBefore ? { effectiveBefore: draft.effectiveBefore } : {}),
          after: draft.after,
          ...(draft.preserveSensitiveFields ? { preserveSensitiveFields: draft.preserveSensitiveFields } : {}),
          expectedRecordRevision: draft.expectedRecordRevision,
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
            <Tag color="blue">Region: {activeScope.region}</Tag>
            <Tag color="green">Environment: {activeScope.environment}</Tag>
            {activeScope.stage ? <Tag color="gold">Stage: {activeScope.stage}</Tag> : null}
            {activePreviewBucket !== undefined ? <Tag color="purple">Preview Bucket: {activePreviewBucket}</Tag> : null}
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
      <Card size="small" title="配置范围">
        <Flex gap={12} wrap align="end">
          <div>
            <label htmlFor="scope-region">Region</label>
            <Input id="scope-region" aria-label="Region" value={scopeDraft.region} onChange={(event) => setScopeDraft((current) => ({ ...current, region: event.target.value }))} />
          </div>
          <div>
            <label htmlFor="scope-environment">Environment</label>
            <Input id="scope-environment" aria-label="Environment" value={scopeDraft.environment} onChange={(event) => setScopeDraft((current) => ({ ...current, environment: event.target.value }))} />
          </div>
          <div>
            <label htmlFor="scope-stage">Stage</label>
            <Input id="scope-stage" aria-label="Stage" placeholder="可选，例如 blue" value={scopeDraft.stage ?? ""} onChange={(event) => setScopeDraft((current) => ({ ...current, stage: event.target.value }))} />
          </div>
          <div>
            <label htmlFor="preview-bucket">预览 Bucket</label>
            <InputNumber
              id="preview-bucket"
              aria-label="预览 Bucket"
              min={0}
              max={99}
              placeholder="可选 0–99"
              value={previewBucketDraft}
              onChange={(value) => setPreviewBucketDraft(typeof value === "number" ? value : undefined)}
            />
          </div>
          <Button type="primary" onClick={applyScope}>应用范围</Button>
          <Typography.Text type="secondary">预览桶只影响诊断视图，不改变真实客户端分桶。</Typography.Text>
        </Flex>
      </Card>
	  <Card size="small" title="查询条件">
		<Flex gap={12} wrap align="end">
		  {page?.interactionFields.filter((field) => field.queryable && !field.sensitive).map((field) => {
			const draft = filterDrafts[field.name] ?? { operator: field.defaultFilterOperator };
			const update = (change: Partial<FilterDraft>) => setFilterDrafts((current) => ({ ...current, [field.name]: { ...draft, ...change } }));
			return (
			  <div key={field.name} className="operation-filter-field">
				<Typography.Text>{field.displayName}</Typography.Text>
				<Space.Compact block>
				  {field.allowedFilterOperators.length > 1 ? (
					<Select aria-label={`${field.displayName} 操作符`} value={draft.operator} options={field.allowedFilterOperators.map((operator) => ({ value: operator, label: filterOperatorLabel(operator) }))} onChange={(operator) => update({ operator, value: undefined, lower: undefined, upper: undefined })} />
				  ) : null}
				  {filterEditor(field, draft, update)}
				</Space.Compact>
			  </div>
			);
		  })}
		  <Button type="primary" onClick={() => void submitQuery()} disabled={loading}>查询</Button>
		  <Button onClick={() => void resetQuery()} disabled={loading}>重置</Button>
		</Flex>
	  </Card>
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
		  loading={loading}
		  pagination={{ current: page?.page.number ?? 1, pageSize: page?.page.size ?? 20, total: page?.page.totalNumber ?? 0, onChange: (number, size) => void queryData(activeConditions, number, size) }}
          locale={{ emptyText: "当前 Environment 尚无配置" }}
        />
      </Card>

      <Modal title={editingRow ? "修改配置" : "新增配置"} open={addOpen} onCancel={() => { setAddOpen(false); setEditingRow(undefined); }} onOk={() => void addDraft()} okText="加入草稿" destroyOnHidden>
        <Form form={form} layout="vertical" preserve={false}>
          {page?.interactionFields.filter((field) => field.editable || field.autoFill).map((field) => (
            field.autoFill ? (
              <Form.Item key={field.name} label={field.displayName} extra={`${field.description}${field.description ? "；" : ""}提交时由服务端生成（${autoFillLabel(field.autoFill.source)}）`}>
                <Input disabled value="由服务端生成" />
              </Form.Item>
            ) : editingRow && field.sensitive ? (
              <Form.Item key={field.name} label={field.displayName} extra={field.description}>
                <Space orientation="vertical" className="operation-full-width">
                  <Checkbox
                    aria-label={`替换 ${field.displayName}`}
                    checked={Boolean(replaceSensitive[field.name])}
                    onChange={(event) => setReplaceSensitive((current) => ({ ...current, [field.name]: event.target.checked }))}
                  >
                    替换现有敏感值
                  </Checkbox>
                  {replaceSensitive[field.name] ? (
                    <Form.Item name={field.name} noStyle rules={fieldRules({ ...field, required: true })}>
                      <Input.Password aria-label={field.displayName} autoComplete="new-password" />
                    </Form.Item>
                  ) : <Tag>保持原值</Tag>}
                </Space>
              </Form.Item>
            ) : (
              <Form.Item
                key={field.name}
                name={field.name}
                label={field.displayName}
                extra={field.description}
                valuePropName={field.uiControl === "BOOLEAN" ? "checked" : "value"}
                rules={fieldRules(field)}
              >
                {editor(field, Boolean(editingRow && field.keyField))}
              </Form.Item>
            )
          ))}
        </Form>
      </Modal>

      <Modal
        title={`临时查看${revealTarget ? ` · ${revealTarget.field.displayName}` : ""}`}
        open={revealTarget !== undefined}
        okText="确认临时查看"
        okButtonProps={{ disabled: revealReason.trim() === "" }}
        confirmLoading={revealing}
        onOk={() => void revealSensitive()}
        onCancel={() => { setRevealTarget(undefined); setRevealReason(""); }}
        destroyOnHidden
      >
        <Space orientation="vertical" className="operation-full-width">
          <Alert type="warning" showIcon title="敏感值仅在当前页面临时显示，最长 60 秒；本次查看会记录审计。" />
          <label htmlFor="sensitive-reveal-reason">查看原因</label>
          <Input.TextArea
            id="sensitive-reveal-reason"
            aria-label="查看原因"
            value={revealReason}
            onChange={(event) => setRevealReason(event.target.value)}
            placeholder="说明本次查看的业务原因"
            autoSize={{ minRows: 2, maxRows: 4 }}
          />
        </Space>
      </Modal>

      <Drawer title="审阅差异与发布" size="large" open={reviewOpen} onClose={() => setReviewOpen(false)}>
        <Space orientation="vertical" size={16} className="operation-review">
          <Alert type="info" showIcon title="所有修改仍在浏览器草稿中；创建发布单不会立即改写配置。" />
          {drafts.map((draft, index) => (
            <Card key={draft.id} size="small" title={`${draft.action} #${index + 1}`}>
              <Descriptions column={1} size="small">
                {projectedFields.map((field) => (
                  <Descriptions.Item key={field.name} label={field.displayName}>
                    {field.sensitive ? (
                      draft.preserveSensitiveFields?.includes(field.name)
                        ? <Tag>{field.displayName}：保持原值</Tag>
                        : <Tag color="orange">已设置新的敏感值</Tag>
                    ) : (
                      <Space wrap>
                        {draft.baseBefore ? <Tag>Base {draft.baseBefore[field.name] ?? "—"}</Tag> : null}
                        {draft.effectiveBefore ? <Tag color="blue">Effective {draft.effectiveBefore[field.name] ?? "—"}</Tag> : null}
                        <Tag color="green">After {draft.after[field.name] ?? "—"}</Tag>
                      </Space>
                    )}
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
              {rolloutSteps.length > 0 ? (
                <Card size="small" title="灰度覆盖">
                  <Typography.Text strong>灰度覆盖 {rolloutCoverage} / 100</Typography.Text>
                  <Progress percent={rolloutCoverage} status={rolloutCoverage === 100 ? "success" : "active"} />
                  <Space wrap>
                    {rolloutSteps.flatMap((step) => (step.rolloutRanges ?? []).map((range) => (
                      <Tag key={`${step.code}-${range.start}-${range.end}`} color={step.status === "EXECUTED" ? "blue" : "default"}>
                        Bucket {range.start}–{range.end}
                      </Tag>
                    )))}
                  </Space>
                </Card>
              ) : null}
              {compareSteps.map((step) => {
                const comparison = step.compareResult!;
                const matched = comparison.diffKeys.length === 0 && comparison.expectedDigest.value === comparison.actualDigest.value;
                return (
                  <Card key={step.code} size="small" title={`对比验证 · ${step.code}`}>
                    <Alert type={matched ? "success" : "error"} showIcon title={matched ? "对比一致" : `发现 ${comparison.diffKeys.length} 条差异`} />
                    <Descriptions column={1} size="small">
                      <Descriptions.Item label="Expected digest"><Typography.Text code>{comparison.expectedDigest.value}</Typography.Text></Descriptions.Item>
                      <Descriptions.Item label="Actual digest"><Typography.Text code>{comparison.actualDigest.value}</Typography.Text></Descriptions.Item>
                      <Descriptions.Item label="检查时间">{comparison.checkedAt}</Descriptions.Item>
                      {comparison.diffKeys.length > 0 ? (
                        <Descriptions.Item label="差异记录">
                          <Space wrap>{comparison.diffKeys.map((key) => <Tag key={key} color="red">{key}</Tag>)}</Space>
                        </Descriptions.Item>
                      ) : null}
                    </Descriptions>
                  </Card>
                );
              })}
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

function editor(field: InteractionField, disabled = false) {
  if (field.sensitive) return <Input.Password disabled={disabled} autoComplete="new-password" />;
  switch (field.uiControl) {
	case "TIME":
		return <Input type="datetime-local" disabled={disabled} />;
    case "NUMBER":
      return <InputNumber stringMode className="operation-full-width" disabled={disabled} />;
    case "BOOLEAN":
      return <Checkbox disabled={disabled}>启用</Checkbox>;
    case "SELECT":
      return <Select disabled={disabled} options={field.options.map((option) => ({ value: option.code, label: option.label, disabled: option.disabled }))} />;
    case "TEXTAREA":
    case "JSON":
      return <Input.TextArea disabled={disabled} autoSize={{ minRows: 3, maxRows: 8 }} />;
    default:
      return <Input disabled={disabled} />;
  }
}

function filterEditor(field: InteractionField, draft: FilterDraft, update: (change: Partial<FilterDraft>) => void) {
	if (draft.operator === "CLOSED_RANGE" || draft.operator === "OPEN_RANGE") {
		return (
		  <>
			<Input aria-label={`${field.displayName} 下界`} placeholder="下界" value={draft.lower ?? ""} onChange={(event) => update({ lower: event.target.value })} />
			<Input aria-label={`${field.displayName} 上界`} placeholder="上界" value={draft.upper ?? ""} onChange={(event) => update({ upper: event.target.value })} />
		  </>
		);
	}
	if (draft.operator === "IN" || draft.operator === "NOT_IN") {
		return <Input aria-label={`筛选 ${field.displayName}`} placeholder="多个值用逗号分隔" value={draft.value ?? ""} onChange={(event) => update({ value: event.target.value })} />;
	}
	if (field.uiControl === "SELECT") {
		return <Select allowClear aria-label={`筛选 ${field.displayName}`} value={draft.value} options={field.options.map((option) => ({ value: option.code, label: option.label, disabled: option.disabled }))} onChange={(value) => update({ value })} />;
	}
	if (field.uiControl === "BOOLEAN") {
		return <Select allowClear aria-label={`筛选 ${field.displayName}`} value={draft.value} options={[{ value: "true", label: "是" }, { value: "false", label: "否" }]} onChange={(value) => update({ value })} />;
	}
	if (field.uiControl === "NUMBER") {
		return <InputNumber stringMode aria-label={`筛选 ${field.displayName}`} value={draft.value} onChange={(value) => update({ value: value === null ? undefined : String(value) })} />;
	}
	if (field.uiControl === "TIME") {
		return <Input type="datetime-local" aria-label={`筛选 ${field.displayName}`} value={draft.value ?? ""} onChange={(event) => update({ value: event.target.value })} />;
	}
	return <Input aria-label={`筛选 ${field.displayName}`} value={draft.value ?? ""} onChange={(event) => update({ value: event.target.value })} />;
}

function buildConditions(fields: InteractionField[], drafts: Record<string, FilterDraft>): QueryCondition[] {
	const conditions: QueryCondition[] = [];
	for (const field of fields) {
		if (!field.queryable || field.sensitive) continue;
		const draft = drafts[field.name];
		if (!draft) continue;
		if (draft.operator === "CLOSED_RANGE" || draft.operator === "OPEN_RANGE") {
			const lower = canonicalFilterValue(field, draft.lower);
			const upper = canonicalFilterValue(field, draft.upper);
			if (lower === undefined && upper === undefined) continue;
			conditions.push({ field: field.name, operator: draft.operator, ...(lower === undefined ? {} : { lower }), ...(upper === undefined ? {} : { upper }) });
			continue;
		}
		if (draft.operator === "IN" || draft.operator === "NOT_IN") {
			const values = (draft.value ?? "").split(",").map((value) => canonicalFilterValue(field, value)).filter((value): value is string => value !== undefined);
			if (values.length === 0) continue;
			conditions.push({ field: field.name, operator: draft.operator, set: [...new Set(values)] });
			continue;
		}
		const value = canonicalFilterValue(field, draft.value);
		if (value !== undefined) conditions.push({ field: field.name, operator: draft.operator, value });
	}
	return conditions;
}

function canonicalFilterValue(field: InteractionField, source?: string) {
	const value = source?.trim();
	if (!value) return undefined;
	return toCanonicalInput(field, value);
}

function filterOperatorLabel(operator: FilterOperator) {
	switch (operator) {
		case "EXACT": return "等于";
		case "CONTAINS": return "包含";
		case "CLOSED_RANGE": return "闭区间";
		case "OPEN_RANGE": return "开区间";
		case "IN": return "属于";
		case "NOT_IN": return "不属于";
	}
}

function sameSnapshot(left: PageResult, right: PageResult) {
	return left.snapshot.serverEpoch === right.snapshot.serverEpoch
		&& left.snapshot.serverInstanceId === right.snapshot.serverInstanceId
		&& left.snapshot.snapshotInstance === right.snapshot.snapshotInstance
		&& left.snapshot.snapshotGeneration === right.snapshot.snapshotGeneration;
}

function revealCellKey(recordKey: string, fieldName: string) {
  return `${recordKey}\u0000${fieldName}`;
}

function renderValue(field: InteractionField, value?: string) {
  if (field.sensitive && value !== undefined) return <Tag>••••••</Tag>;
  if (field.type === "BOOL") return <Tag color={value === "true" ? "green" : "default"}>{value === "true" ? "是" : "否"}</Tag>;
  return value ?? "—";
}

function fromCanonicalDefault(field: InteractionField): unknown {
  if (field.type === "BOOL") return field.defaultValue === "true";
	if (field.type === "TIMESTAMP" && field.defaultValue) return toDatetimeLocal(field.defaultValue);
  return field.defaultValue;
}

function fromCanonicalValue(field: InteractionField, value?: string): unknown {
  if (field.type === "BOOL") return value === "true";
	if (field.type === "TIMESTAMP" && value) return toDatetimeLocal(value);
  return value;
}

function toCanonicalInput(field: InteractionField, value: unknown): string {
  if (field.type === "BOOL") return value ? "true" : "false";
	if (field.type === "TIMESTAMP") {
		const parsed = new Date(String(value));
		if (!Number.isNaN(parsed.valueOf())) return parsed.toISOString();
	}
  return String(value);
}

function fieldRules(field: InteractionField) {
  return [
    { required: field.required, message: `${field.displayName} 为必填项` },
    {
      validator: async (_: unknown, input: unknown) => {
        if (input === undefined || input === null || input === "") return;
        const value = field.type === "BOOL" ? (input ? "true" : "false") : String(input);
        for (const rule of field.validationRules) {
		  if (!passesValidationRule(field, rule, value)) throw new Error(rule.message);
        }
      },
    },
  ];
}

function passesValidationRule(field: InteractionField, rule: InteractionField["validationRules"][number], value: string) {
  switch (rule.kind) {
    case "REQUIRED": return value.length > 0;
    case "ENUM": {
      try { return (JSON.parse(rule.params.values ?? "[]") as string[]).includes(value); } catch { return true; }
    }
    case "REGEX": {
      try { return new RegExp(rule.params.pattern ?? "").test(value); } catch { return true; }
    }
	case "MIN": return compareRuleValue(field.type, value, rule.params.value) >= 0;
	case "MAX": return compareRuleValue(field.type, value, rule.params.value) <= 0;
    case "MIN_LENGTH": return [...value].length >= Number(rule.params.value);
    case "MAX_LENGTH": return [...value].length <= Number(rule.params.value);
    default: return true;
  }
}

function compareRuleValue(type: InteractionField["type"], left: string, right = "") {
	if (type === "INT64") {
		try {
			const leftValue = BigInt(left);
			const rightValue = BigInt(right);
			return leftValue < rightValue ? -1 : leftValue > rightValue ? 1 : 0;
		} catch { return 0; }
	}
	const leftValue = type === "TIMESTAMP" ? Date.parse(left) : Number(left);
	const rightValue = type === "TIMESTAMP" ? Date.parse(right) : Number(right);
	if (!Number.isFinite(leftValue) || !Number.isFinite(rightValue)) return 0;
	return leftValue < rightValue ? -1 : leftValue > rightValue ? 1 : 0;
}

function toDatetimeLocal(value: string) {
	const date = new Date(value);
	if (Number.isNaN(date.valueOf())) return value;
	const local = new Date(date.valueOf() - date.getTimezoneOffset() * 60_000);
	return local.toISOString().slice(0, 19);
}

function autoFillLabel(source: NonNullable<InteractionField["autoFill"]>["source"]) {
  switch (source) {
    case "ACTOR_SUBJECT": return "当前操作人账号";
    case "ACTOR_NAME": return "当前操作人姓名";
    case "CURRENT_TIME": return "当前时间";
    case "CONSTANT": return "系统常量";
    case "UUID": return "UUID";
  }
}

function actionLabel(action: ReleaseAction, release: ReleaseDetail) {
  if (action === "APPROVE") return "批准";
  if (action === "REJECT") return "驳回";
  if (action === "ADVANCE") return "推进下一步";
  if (action === "ROLLBACK") return "回滚当前步骤";
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
    case "OVERLAY_APPLY":
      return "范围覆盖";
    case "PERCENT_ROLLOUT":
      return "百分比灰度";
    case "COMPARE":
      return "对比验证";
    case "COMPLETE":
      return "完成";
    default:
      return type;
  }
}

function stepVisualStatus(status: string, current: boolean): "wait" | "process" | "finish" | "error" {
  if (status === "REJECTED" || status === "ROLLED_BACK") return "error";
  if (status === "EXECUTED" || status === "APPROVED") return "finish";
  if (current && (status === "PENDING" || status === "EXECUTING")) return "process";
  return "wait";
}

function coveredBuckets(steps: ReleaseDetail["steps"]): number {
  const selected = Array.from({ length: 100 }, () => false);
  for (const step of steps) {
    for (const range of step.rolloutRanges ?? []) {
      for (let bucket = Math.max(0, range.start); bucket <= Math.min(99, range.end); bucket += 1) {
        selected[bucket] = true;
      }
    }
  }
  return selected.filter(Boolean).length;
}
