import type { CreateReleaseRequest, OperationApi, PageResult, ReleaseDetail, RevealSensitiveRequest } from "./types";

async function requestJson<T>(path: string, body: unknown, headers: Record<string, string> = {}): Promise<T> {
  const response = await fetch(path, {
    method: "POST",
    credentials: "same-origin",
    headers: {
      "Content-Type": "application/json",
      ...headers,
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
    requestJson<ReleaseDetail>("/api/v1/releases", request, { "Idempotency-Key": idempotencyKey }),
  revealSensitive: (request: RevealSensitiveRequest, requestId: string) =>
    requestJson<{ value: string; expiresAt: string }>("/api/v1/sensitive-fields/reveal", request, { "X-Request-ID": requestId }),
  actOnRelease: (orderId, actionRequestId, request) =>
    requestJson<ReleaseDetail>(`/api/v1/releases/${encodeURIComponent(orderId)}/actions`, request, { "Idempotency-Key": actionRequestId }),
};
