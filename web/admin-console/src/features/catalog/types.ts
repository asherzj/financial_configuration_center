export type FieldType = "STRING" | "INT64" | "FLOAT64" | "BOOL" | "TIMESTAMP" | "JSON";

export interface CollectionField {
	name: string;
	displayName: string;
	type: FieldType;
	required: boolean;
	sensitive: boolean;
	defaultValue?: string;
	description: string;
	displayOrder: number;
	validationRules: Array<{ kind: string; params: Record<string, string>; message: string }>;
}

export interface CollectionMetadata {
	name: string;
	description: string;
	fields: CollectionField[];
	keyFields: string[];
	sdkDeliveryEnabled: boolean;
	schemaVersion: number;
	status: "ENABLED" | "DISABLED";
	configRevision: number;
}

export type CollectionInput = Omit<CollectionMetadata, "configRevision">;

export interface SubscriptionMetadata {
	id: string;
	consumerId: string;
	collection: string;
	indexName: string;
	indexFields: string[];
	cardinality: "ONE_TO_ONE" | "ONE_TO_MANY";
	enabled: boolean;
	configRevision: number;
}

export type SubscriptionInput = Omit<SubscriptionMetadata, "id" | "configRevision"> & { id?: string };

export interface CatalogApi {
	listCollections(page: number, size: number): Promise<{ collections: CollectionMetadata[]; page: PageMetadata }>;
	createCollection(input: CollectionInput): Promise<CollectionMetadata>;
	updateCollection(name: string, expectedRevision: number, input: CollectionInput): Promise<CollectionMetadata>;
	listSubscriptions(filters: { consumerId?: string; collection?: string }, page: number, size: number): Promise<{ subscriptions: SubscriptionMetadata[]; page: PageMetadata }>;
	createSubscription(input: SubscriptionInput): Promise<SubscriptionMetadata>;
	updateSubscription(id: string, expectedRevision: number, input: SubscriptionInput): Promise<SubscriptionMetadata>;
}

export interface PageMetadata { number: number; size: number; totalNumber: number; totalPages: number }
