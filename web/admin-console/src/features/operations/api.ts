import type { CreateReleaseRequest, OperationApi, PageResult, ReleaseDetail } from "./types";

async function requestJson<T>(path: string, body: unknown, idempotencyKey?: string): Promise<T> {
  const response = await fetch(path, {
    method: "POST",
    credentials: "same-origin",
    headers: {
      "Content-Type": "application/json",
      ...(idempotencyKey ? { "Idempotency-Key": idempotencyKey } : {}),
    },
    body: JSON.stringify(body),
  });
  if (!response.ok) {
    const error = (await response.json().catch(() => null)) as { message?: string } | null;
    throw new Error(error?.message ?? `Request failed with status ${response.status}`);
  }
  return (await response.json()) as T;
}

export const operationApi: OperationApi = {
  queryPage: (request) => requestJson<PageResult>("/api/v1/query-page", request),
  createRelease: (request: CreateReleaseRequest, idempotencyKey: string) =>
    requestJson<ReleaseDetail>("/api/v1/releases", request, idempotencyKey),
  actOnRelease: (orderId, actionRequestId, request) =>
    requestJson<ReleaseDetail>(`/api/v1/releases/${encodeURIComponent(orderId)}/actions`, request, actionRequestId),
};
