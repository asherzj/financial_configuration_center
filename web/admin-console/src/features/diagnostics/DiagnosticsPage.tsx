import { Alert, Button, Card, Descriptions, Flex, Input, Modal, Select, Space, Table, Tag, Typography, message } from "antd";
import { useCallback, useEffect, useState } from "react";

import { diagnosticsApi } from "./api";
import type { DiagnosticsApi, OutboxEventMetadata, OutboxEventPage, OutboxStatus } from "./types";

const pageSize = 20;

export function DiagnosticsPage({ api = diagnosticsApi }: { api?: DiagnosticsApi }) {
	const [status, setStatus] = useState<OutboxStatus | undefined>("DEAD_LETTER");
	const [pageNumber, setPageNumber] = useState(1);
	const [page, setPage] = useState<OutboxEventPage>();
	const [loading, setLoading] = useState(true);
	const [error, setError] = useState<string>();
	const [replayTarget, setReplayTarget] = useState<OutboxEventMetadata>();
	const [reason, setReason] = useState("");
	const [confirmation, setConfirmation] = useState("");
	const [replaying, setReplaying] = useState(false);

	const load = useCallback(async () => {
		setLoading(true);
		setError(undefined);
		try {
			setPage(await api.listOutboxEvents(status, pageNumber, pageSize));
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : "加载 Outbox 诊断失败");
		} finally {
			setLoading(false);
		}
	}, [api, pageNumber, status]);

	useEffect(() => { void load(); }, [load]);

	const closeReplay = () => {
		setReplayTarget(undefined);
		setReason("");
		setConfirmation("");
	};

	const replay = async () => {
		if (!replayTarget || reason.trim() === "" || confirmation !== replayPhrase(replayTarget.id)) return;
		setReplaying(true);
		try {
			await api.replayOutboxEvent(replayTarget.id, {
				expectedEventRevision: replayTarget.leaseRevision,
				reason: reason.trim(),
				confirmation,
			});
			closeReplay();
			await load();
			message.success("死信事件已重新进入待投递队列");
		} catch (cause) {
			message.error(cause instanceof Error ? cause.message : "重放失败");
		} finally {
			setReplaying(false);
		}
	};

	return (
		<Space orientation="vertical" size={16} className="operation-page">
			<Flex justify="space-between" align="center" gap={16}>
				<div>
					<Typography.Title level={1}>运行诊断</Typography.Title>
					<Typography.Paragraph type="secondary">仅展示投递元数据，不展示配置正文或事件 payload。</Typography.Paragraph>
				</div>
				<Button onClick={() => void load()} loading={loading}>刷新</Button>
			</Flex>
			{error ? <Alert type="error" showIcon title={error} /> : null}
			<Card title="Outbox 投递状态">
				<Space orientation="vertical" className="operation-full-width">
					<Select
						aria-label="Outbox 状态"
						value={status ?? "ALL"}
						onChange={(value) => { setStatus(value === "ALL" ? undefined : value as OutboxStatus); setPageNumber(1); }}
						options={[
							{ value: "ALL", label: "全部状态" },
							{ value: "DEAD_LETTER", label: "死信" },
							{ value: "PENDING", label: "待投递" },
							{ value: "PROCESSING", label: "投递中" },
							{ value: "SENT", label: "已发送" },
						]}
					/>
					<Table<OutboxEventMetadata>
						rowKey="id"
						loading={loading}
						dataSource={page?.events ?? []}
						columns={[
							{ title: "事件 ID", dataIndex: "id", render: (value: string) => <Typography.Text code>{value}</Typography.Text> },
							{ title: "事件类型", dataIndex: "eventType" },
							{ title: "状态", dataIndex: "status", render: (value: OutboxStatus) => <Tag color={value === "DEAD_LETTER" ? "red" : value === "SENT" ? "green" : "blue"}>{value}</Tag> },
							{ title: "Lease revision", dataIndex: "leaseRevision" },
							{ title: "尝试次数", dataIndex: "attempts" },
							{ title: "下次尝试", dataIndex: "nextAttemptAt" },
							{ title: "错误摘要", dataIndex: "lastError", render: (value?: string) => value || "—" },
							{ title: "操作", render: (_: unknown, event) => event.status === "DEAD_LETTER" ? <Button danger onClick={() => setReplayTarget(event)}>重放死信</Button> : null },
						]}
						pagination={{
							current: page?.page.number ?? pageNumber,
							pageSize,
							total: page?.page.totalNumber ?? 0,
							showSizeChanger: false,
							onChange: setPageNumber,
						}}
					/>
				</Space>
			</Card>
			<Modal
				title="重放死信事件"
				open={replayTarget !== undefined}
				onCancel={closeReplay}
				footer={[
					<Button key="cancel" onClick={closeReplay}>取消</Button>,
					<Button key="replay" danger type="primary" loading={replaying} disabled={!replayTarget || reason.trim() === "" || confirmation !== replayPhrase(replayTarget.id)} onClick={() => void replay()}>确认重放</Button>,
				]}
			>
				<Space orientation="vertical" className="operation-full-width">
					<Alert type="warning" showIcon title="重放会保留原事件 ID、payload 和幂等键，只重置投递状态。" />
					{replayTarget ? <Descriptions column={1} size="small"><Descriptions.Item label="事件 ID">{replayTarget.id}</Descriptions.Item><Descriptions.Item label="当前 Lease revision">{replayTarget.leaseRevision}</Descriptions.Item></Descriptions> : null}
					<label htmlFor="replay-reason">重放原因</label>
					<Input.TextArea id="replay-reason" aria-label="重放原因" value={reason} onChange={(event) => setReason(event.target.value)} />
					<Typography.Text>请输入确认短语：<Typography.Text code>{replayTarget ? replayPhrase(replayTarget.id) : ""}</Typography.Text></Typography.Text>
					<Input aria-label="重放确认短语" value={confirmation} onChange={(event) => setConfirmation(event.target.value)} />
				</Space>
			</Modal>
		</Space>
	);
}

function replayPhrase(eventId: string) { return `REPLAY ${eventId}`; }
