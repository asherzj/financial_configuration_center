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
	listModels(filters: { collection?: string }, page: number, size: number): Promise<{ models: ModelMetadata[]; page: PageMetadata }>;
	previewModel(input: ModelInput): Promise<ModelPreview>;
	createModel(input: ModelInput): Promise<ModelMetadata>;
	updateModel(code: string, expectedRevision: number, input: ModelInput): Promise<ModelMetadata>;
	listTemplates(filters: { modelCode?: string }, page: number, size: number): Promise<{ templates: ReleaseTemplateMetadata[]; page: PageMetadata }>;
	createTemplate(input: ReleaseTemplateInput): Promise<ReleaseTemplateMetadata>;
}

export interface PageMetadata { number: number; size: number; totalNumber: number; totalPages: number }

export interface ModelMetadata {
	code: string;
	name: string;
	collection: string;
	definition: Record<string, unknown>;
	enabled: boolean;
	configRevision: number;
}
export type ModelInput = Omit<ModelMetadata, "configRevision">;
export interface ModelPreview { valid: boolean; issues: Array<{ code: string; path: string; message: string }>; normalizedDefinition?: Record<string, unknown> }

export interface ReleaseTemplateMetadata {
	code: string;
	name: string;
	modelCode: string;
	releaseTypeCode: string;
	version: number;
	finalEffect: "BASE_FINAL" | "OVERLAY_FINAL";
	schedulingAllowed: boolean;
	maxScheduleWindowSeconds: number;
	document: Record<string, unknown>;
	allowedRoles: string[];
	enabled: boolean;
}
export type ReleaseTemplateInput = Omit<ReleaseTemplateMetadata, "version">;
