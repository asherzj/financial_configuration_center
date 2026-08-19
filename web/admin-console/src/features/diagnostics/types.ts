export type OutboxStatus = "PENDING" | "PROCESSING" | "SENT" | "DEAD_LETTER";

export interface OutboxEventMetadata {
	id: string;
	sequenceNo: number;
	eventType: string;
	status: OutboxStatus;
	leaseRevision: number;
	attempts: number;
	nextAttemptAt: string;
	lastError?: string;
}

export interface OutboxEventPage {
	events: OutboxEventMetadata[];
	page: { number: number; size: number; totalNumber: number; totalPages: number };
}

export interface SnapshotDiagnostics {
	snapshot: { serverEpoch: string; serverInstanceId: string; snapshotInstance: string; generation: number; publishedAt: string };
	environment: string;
	collections: Array<{ name: string; revision: number; digest: { algorithm: string; value: string } }>;
	failedDependencyGroups: string[][];
	lastErrorCode: string;
}

export interface AuditRecordMetadata {
	id: number;
	occurredAt: string;
	principalSubject: string;
	action: string;
	resourceType: string;
	resourceId: string;
	scope: { region: string; environment: string; stage: string };
	result: "SUCCEEDED" | "FAILED";
	traceId: string;
}

export interface AuditRecordPage {
	records: AuditRecordMetadata[];
	page: { number: number; size: number; totalNumber: number; totalPages: number };
}

export interface AuditFilters {
	principalSubject?: string;
	resourceType?: string;
	resourceId?: string;
	from?: string;
	until?: string;
}

export interface DiagnosticsApi {
	getSnapshotDiagnostics(): Promise<SnapshotDiagnostics>;
	listAuditRecords(filters: AuditFilters, page: number, size: number): Promise<AuditRecordPage>;
	listOutboxEvents(status: OutboxStatus | undefined, page: number, size: number): Promise<OutboxEventPage>;
	replayOutboxEvent(eventId: string, request: { expectedEventRevision: number; reason: string; confirmation: string }): Promise<{ event: OutboxEventMetadata }>;
}
