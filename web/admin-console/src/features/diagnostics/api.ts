import type { AuditFilters, AuditRecordPage, DiagnosticsApi, OutboxEventPage, OutboxStatus } from "./types";

async function requestJson<T>(path: string, init?: RequestInit): Promise<T> {
	const response = await fetch(path, { credentials: "same-origin", ...init });
	if (!response.ok) {
		const error = (await response.json().catch(() => null)) as { message?: string } | null;
		throw new Error(error?.message ?? `Request failed with status ${response.status}`);
	}
	return (await response.json()) as T;
}

export const diagnosticsApi: DiagnosticsApi = {
	getSnapshotDiagnostics: () => requestJson("/api/v1/diagnostics/snapshot"),
	listAuditRecords: (filters: AuditFilters, page: number, size: number) => {
		const search = new URLSearchParams({ page: String(page), size: String(size) });
		for (const [name, value] of Object.entries(filters)) {
			if (value) search.set(name, value);
		}
		return requestJson<AuditRecordPage>(`/api/v1/audit-records?${search.toString()}`);
	},
	listOutboxEvents: (status: OutboxStatus | undefined, page: number, size: number) => {
		const search = new URLSearchParams({ page: String(page), size: String(size) });
		if (status) search.set("status", status);
		return requestJson<OutboxEventPage>(`/api/v1/outbox-events?${search.toString()}`);
	},
	replayOutboxEvent: (eventId, request) => requestJson(`/api/v1/outbox-events/${encodeURIComponent(eventId)}/replay`, {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify(request),
	}),
};
