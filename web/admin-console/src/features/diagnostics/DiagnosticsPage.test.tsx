import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { DiagnosticsPage } from "./DiagnosticsPage";
import type { DiagnosticsApi, OutboxEventPage } from "./types";

afterEach(cleanup);

describe("DiagnosticsPage", () => {
	it("lists payload-free dead-letter metadata and requires exact replay confirmation", async () => {
		const deadLetter = {
			id: "20000000-0000-4000-8000-000000000001",
			sequenceNo: 12,
			eventType: "CONFIGURATION_CHANGED",
			status: "DEAD_LETTER" as const,
			leaseRevision: 6,
			attempts: 20,
			nextAttemptAt: "2026-08-20T08:00:00Z",
			lastError: "delivery failed",
		};
		const initial: OutboxEventPage = { events: [deadLetter], page: { number: 1, size: 20, totalNumber: 1, totalPages: 1 } };
		const afterReplay: OutboxEventPage = { events: [], page: { number: 1, size: 20, totalNumber: 0, totalPages: 0 } };
		const listOutboxEvents = vi.fn().mockResolvedValueOnce(initial).mockResolvedValue(afterReplay);
		const replayOutboxEvent = vi.fn().mockResolvedValue({ event: { ...deadLetter, status: "PENDING", leaseRevision: 7 } });
		const listAuditRecords = vi.fn().mockResolvedValue({
			records: [{
				id: 7, occurredAt: "2026-08-20T10:00:00Z", principalSubject: "alice", action: "UPDATE",
				resourceType: "COLLECTION", resourceId: "payment_routes", scope: { region: "cn", environment: "production", stage: "" },
				result: "SUCCEEDED", traceId: "trace-a",
			}],
			page: { number: 1, size: 20, totalNumber: 1, totalPages: 1 },
		});
		const api: DiagnosticsApi = {
			getSnapshotDiagnostics: vi.fn().mockResolvedValue({
				snapshot: { serverEpoch: "epoch", serverInstanceId: "server", snapshotInstance: "instance", generation: 3, publishedAt: "2026-08-20T08:00:00Z" },
				environment: "production",
				collections: [{ name: "payment_routes", revision: 8, digest: { algorithm: "SHA-256", value: "safe-digest" } }],
				failedDependencyGroups: [["payment_routes", "priorities"]],
				lastErrorCode: "",
			}),
			listAuditRecords,
			listOutboxEvents,
			replayOutboxEvent,
		};

		render(<DiagnosticsPage api={api} />);
		expect(await screen.findByText("delivery failed")).toBeInTheDocument();
		expect(await screen.findByText("safe-digest")).toBeInTheDocument();
		expect(screen.getByText("payment_routes ↔ priorities")).toBeInTheDocument();
		expect(await screen.findByText("trace-a")).toBeInTheDocument();
		expect(screen.queryByText(/payload.*must-not-leak/i)).not.toBeInTheDocument();
		expect(screen.queryByText(/before-secret|after-secret|metadata-secret/i)).not.toBeInTheDocument();
		fireEvent.change(screen.getByLabelText("审计主体"), { target: { value: "alice" } });
		fireEvent.change(screen.getByLabelText("审计资源类型"), { target: { value: "COLLECTION" } });
		fireEvent.click(screen.getByRole("button", { name: "筛选审计" }));
		await waitFor(() => expect(listAuditRecords).toHaveBeenLastCalledWith({ principalSubject: "alice", resourceType: "COLLECTION" }, 1, 20));
		expect(listOutboxEvents).toHaveBeenCalledWith("DEAD_LETTER", 1, 20);
		fireEvent.click(screen.getByRole("button", { name: "重放死信" }));
		fireEvent.change(screen.getByLabelText("重放原因"), { target: { value: "downstream recovered" } });
		const confirm = screen.getByRole("button", { name: "确认重放" });
		expect(confirm).toBeDisabled();
		fireEvent.change(screen.getByLabelText("重放确认短语"), { target: { value: `REPLAY ${deadLetter.id}` } });
		expect(confirm).not.toBeDisabled();
		fireEvent.click(confirm);
		await waitFor(() => expect(replayOutboxEvent).toHaveBeenCalledWith(deadLetter.id, {
			expectedEventRevision: 6,
			reason: "downstream recovered",
			confirmation: `REPLAY ${deadLetter.id}`,
		}));
		await waitFor(() => expect(listOutboxEvents).toHaveBeenCalledTimes(2));
	});
});
