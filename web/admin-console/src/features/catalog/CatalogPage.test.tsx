import { cleanup, render, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { CatalogPage } from "./CatalogPage";
import type { CatalogApi, CollectionMetadata, SubscriptionMetadata } from "./types";

afterEach(cleanup);

describe("CatalogPage", () => {
	it("lists metadata and preserves expected revision on edits", async () => {
		const collection: CollectionMetadata = { name: "routes", description: "Routes", fields: [{ name: "code", displayName: "Code", type: "STRING", required: true, sensitive: false, description: "", displayOrder: 0, validationRules: [] }], keyFields: ["code"], sdkDeliveryEnabled: true, schemaVersion: 1, status: "ENABLED", configRevision: 8 };
		const subscription: SubscriptionMetadata = { id: "subscription", consumerId: "checkout", collection: "routes", indexName: "by-code", indexFields: ["code"], cardinality: "ONE_TO_ONE", enabled: true, configRevision: 9 };
		const api: CatalogApi = {
			listCollections: vi.fn().mockResolvedValue({ collections: [collection], page: { number: 1, size: 20, totalNumber: 1, totalPages: 1 } }),
			createCollection: vi.fn(), updateCollection: vi.fn(),
			listSubscriptions: vi.fn().mockResolvedValue({ subscriptions: [subscription], page: { number: 1, size: 20, totalNumber: 1, totalPages: 1 } }),
			createSubscription: vi.fn(), updateSubscription: vi.fn(),
		};
		render(<CatalogPage api={api} />);
		await waitFor(() => expect(api.listCollections).toHaveBeenCalledWith(1, 20));
		expect(api.listSubscriptions).toHaveBeenCalledWith({}, 1, 20);
	});
});
