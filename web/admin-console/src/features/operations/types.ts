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
  basePresent: boolean;
  baseValues: Record<string, string>;
  changedFields: string[];
}

export interface Scope {
  region: string;
  environment: string;
  stage?: string;
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
  scope: Scope;
  items: Array<{
    action: "ADD" | "MODIFY" | "DELETE";
    baseBefore?: Record<string, string>;
    effectiveBefore?: Record<string, string>;
    after?: Record<string, string>;
    expectedRecordRevision: number;
    expectedCollectionRevision: number;
  }>;
}

export interface ReleaseDetail {
  order: {
    id: string;
    status: "IN_PROGRESS" | "SUCCEEDED" | "REJECTED" | "ROLLED_BACK";
    currentStep: string;
    currentStepType: "MANUAL_REVIEW" | "OVERLAY_APPLY" | "PERCENT_ROLLOUT" | "BASE_APPLY" | "COMPARE" | "COMPLETE";
    currentStepStatus: "PENDING" | "EXECUTING" | "EXECUTED" | "APPROVED" | "REJECTED" | "ROLLED_BACK";
    entityRevision: number;
  };
  items: unknown[];
  steps: Array<{
    code: string;
    type: string;
    status: string;
    rolloutRanges?: Array<{ start: number; end: number }>;
    compareResult?: {
      expectedDigest: { algorithm: string; value: string };
      actualDigest: { algorithm: string; value: string };
      diffKeys: string[];
      checkedAt: string;
    };
  }>;
  allowedActions: ReleaseAction[];
}

export type ReleaseAction = "EXECUTE" | "ADVANCE" | "ROLLBACK" | "APPROVE" | "REJECT";

export interface OperationApi {
  queryPage(request: {
    modelCode: string;
    scope: Scope;
    queryType: "ALL" | "ONLY_DATA";
    previewBucket?: number;
  }): Promise<PageResult>;
  createRelease(request: CreateReleaseRequest, idempotencyKey: string): Promise<ReleaseDetail>;
  actOnRelease(
    orderId: string,
    actionRequestId: string,
    request: { action: ReleaseAction; expectedOrderRevision: number; expectedCurrentStep: string; comment?: string },
  ): Promise<ReleaseDetail>;
}
