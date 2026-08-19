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

export interface DiagnosticsApi {
	getSnapshotDiagnostics(): Promise<SnapshotDiagnostics>;
	listOutboxEvents(status: OutboxStatus | undefined, page: number, size: number): Promise<OutboxEventPage>;
	replayOutboxEvent(eventId: string, request: { expectedEventRevision: number; reason: string; confirmation: string }): Promise<{ event: OutboxEventMetadata }>;
}
