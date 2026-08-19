import type { CatalogApi, CollectionInput, CollectionMetadata, SubscriptionInput, SubscriptionMetadata } from "./types";

async function requestJson<T>(path: string, init?: RequestInit): Promise<T> {
	const response = await fetch(path, { credentials: "same-origin", ...init });
	if (!response.ok) {
		const error = (await response.json().catch(() => null)) as { message?: string } | null;
		throw new Error(error?.message ?? `Request failed with status ${response.status}`);
	}
	return (await response.json()) as T;
}

function jsonRequest(method: string, body: unknown, expectedRevision?: number): RequestInit {
	const headers: Record<string, string> = { "Content-Type": "application/json" };
	if (expectedRevision !== undefined) headers["If-Match"] = String(expectedRevision);
	return { method, headers, body: JSON.stringify(body) };
}

export const catalogApi: CatalogApi = {
	listCollections: (page, size) => requestJson(`/api/v1/collections?page=${page}&size=${size}`),
	createCollection: (input: CollectionInput) => requestJson<CollectionMetadata>("/api/v1/collections", jsonRequest("POST", input)),
	updateCollection: (name, expectedRevision, input) => requestJson<CollectionMetadata>(`/api/v1/collections/${encodeURIComponent(name)}`, jsonRequest("PUT", input, expectedRevision)),
	listSubscriptions: (filters, page, size) => {
		const search = new URLSearchParams({ page: String(page), size: String(size) });
		if (filters.consumerId) search.set("consumerId", filters.consumerId);
		if (filters.collection) search.set("collection", filters.collection);
		return requestJson(`/api/v1/subscriptions?${search.toString()}`);
	},
	createSubscription: (input: SubscriptionInput) => requestJson<SubscriptionMetadata>("/api/v1/subscriptions", jsonRequest("POST", input)),
	updateSubscription: (id, expectedRevision, input) => requestJson<SubscriptionMetadata>(`/api/v1/subscriptions/${encodeURIComponent(id)}`, jsonRequest("PUT", input, expectedRevision)),
};
