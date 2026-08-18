export type FieldType = "STRING" | "INT64" | "FLOAT64" | "BOOL" | "TIMESTAMP" | "JSON";
export type UiControl = "INPUT" | "SELECT" | "TIME" | "NUMBER" | "BOOLEAN" | "TEXTAREA" | "JSON";
export type FilterOperator = "EXACT" | "CONTAINS" | "CLOSED_RANGE" | "OPEN_RANGE" | "IN" | "NOT_IN";

export interface InteractionField {
  name: string;
  displayName: string;
  description: string;
  type: FieldType;
  uiControl: UiControl;
  queryable: boolean;
  editable: boolean;
  required: boolean;
  sensitive: boolean;
  projected: boolean;
  keyField: boolean;
  allowedFilterOperators: FilterOperator[];
  defaultFilterOperator: FilterOperator;
  defaultValue?: string;
  displayOrder: number;
  validationRules: Array<{ kind: string; params: Record<string, string>; message: string }>;
  options: Array<{ code: string; label: string; disabled: boolean }>;
}

export interface PageRow {
  recordKey: string;
  recordRevision: number;
  values: Record<string, string>;
  maskedFields: string[];
}

export interface PageResult {
  modelCode: string;
  modelName: string;
  queryType: "ALL" | "ONLY_DATA";
  rows: PageRow[];
  projectionFields: string[];
  interactionFields: InteractionField[];
  releaseTypes: Array<{
    code: string;
    name: string;
    templateCode: string;
    available: boolean;
    unavailableReasonCode?: string;
  }>;
  page: { number: number; size: number; totalNumber: number; totalPages: number };
  snapshot: {
    serverEpoch: string;
    serverInstanceId: string;
    snapshotInstance: string;
    snapshotGeneration: number;
    publishedAt: string;
  };
  modelRevision: number;
  collectionRevision: number;
}

export interface CreateReleaseRequest {
  modelCode: string;
  releaseTypeCode: string;
  description: string;
  scope: { region: string; environment: string; stage?: string };
  items: Array<{
    action: "ADD";
    after: Record<string, string>;
    expectedRecordRevision: 0;
    expectedCollectionRevision: number;
  }>;
}

export interface ReleaseDetail {
  order: {
    id: string;
    status: "IN_PROGRESS" | "SUCCEEDED" | "REJECTED";
    currentStep: string;
    currentStepType: "MANUAL_REVIEW" | "BASE_APPLY" | "COMPARE" | "COMPLETE";
    currentStepStatus: "PENDING" | "EXECUTING" | "EXECUTED" | "APPROVED" | "REJECTED";
    entityRevision: number;
  };
  items: unknown[];
  steps: Array<{ code: string; type: string; status: string }>;
  allowedActions: ReleaseAction[];
}

export type ReleaseAction = "EXECUTE" | "ADVANCE" | "APPROVE" | "REJECT";

export interface OperationApi {
  queryPage(request: {
    modelCode: string;
    scope: { region: string; environment: string };
    queryType: "ALL" | "ONLY_DATA";
  }): Promise<PageResult>;
  createRelease(request: CreateReleaseRequest, idempotencyKey: string): Promise<ReleaseDetail>;
  actOnRelease(
    orderId: string,
    actionRequestId: string,
    request: { action: ReleaseAction; expectedOrderRevision: number; expectedCurrentStep: string; comment?: string },
  ): Promise<ReleaseDetail>;
}
