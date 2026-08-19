package adminbff_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	access "github.com/asherzj/financial_configuration_center/internal/access/application"
	"github.com/asherzj/financial_configuration_center/internal/adminbff"
	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
	"github.com/asherzj/financial_configuration_center/internal/outbox"
	"github.com/asherzj/financial_configuration_center/internal/pagequery"
	"github.com/asherzj/financial_configuration_center/internal/release/application"
	release "github.com/asherzj/financial_configuration_center/internal/release/domain"
)

func TestBrowserBaseReleaseJourneyRoutes(t *testing.T) {
	t.Parallel()

	queries := &queryStub{result: pagequery.Result{
		ModelCode: "model", ModelName: "Model", QueryType: pagequery.TypeAll, PageNumber: 1, PageSize: 20,
		InteractionFields: []pagequery.InteractionField{{
			Name: "id", DisplayName: "ID", Type: catalog.FieldTypeString, UIControl: catalog.UIControlInput,
			AutoFill:        &catalog.AutoFillRule{Field: "id", Source: catalog.AutoFillUUID},
			ValidationRules: []catalog.ValidationRule{{Kind: catalog.ValidationRegex, Params: map[string]string{"pattern": "^[a-z]+$"}, Message: "lowercase"}},
		}},
		ReleaseTypes: []pagequery.ReleaseType{{Code: "direct", Name: "Direct", TemplateCode: "base-final", Available: true}},
	}}
	releases := &releaseStub{
		created: application.OrderView{ID: "order-1", Status: release.OrderInProgress, CurrentStepCode: "base-apply", CurrentStep: release.StepBaseApply, Revision: 1},
		acted:   application.OrderView{ID: "order-1", Status: release.OrderInProgress, CurrentStepCode: "base-apply", CurrentStep: release.StepBaseApply, Revision: 2},
	}
	handler, err := adminbff.New(queries, releases, authenticator{})
	if err != nil {
		t.Fatal(err)
	}

	queryResponse := serveJSON(t, handler, http.MethodPost, "/api/v1/query-page", "", map[string]any{
		"modelCode": "model", "scope": map[string]any{"region": "cn", "environment": "production"}, "queryType": "ALL",
		"conditions": []any{map[string]any{"field": "id", "operator": "EXACT", "value": "active"}},
	})
	if queryResponse.Code != http.StatusOK || queries.last.Region != "cn" || queries.last.Environment != "production" || queries.last.Type != pagequery.TypeAll {
		t.Fatalf("query response=%d command=%+v body=%s", queryResponse.Code, queries.last, queryResponse.Body.String())
	}
	if !bytes.Contains(queryResponse.Body.Bytes(), []byte(`"releaseTypes":[{"available":true,"code":"direct","name":"Direct","templateCode":"base-final"}]`)) {
		t.Fatalf("release type metadata = %s", queryResponse.Body.String())
	}
	if !bytes.Contains(queryResponse.Body.Bytes(), []byte(`"autoFill":{"source":"UUID","value":""}`)) || !bytes.Contains(queryResponse.Body.Bytes(), []byte(`"validationRules":[{"kind":"REGEX","message":"lowercase","params":{"pattern":"^[a-z]+$"}}]`)) {
		t.Fatalf("interaction metadata = %s", queryResponse.Body.String())
	}
	if len(queries.last.Conditions) != 1 || queries.last.Conditions[0].Value == nil || queries.last.Conditions[0].Value.Canonical != "active" {
		t.Fatalf("query conditions = %+v", queries.last.Conditions)
	}

	createResponse := serveJSON(t, handler, http.MethodPost, "/api/v1/releases", "create-id", map[string]any{
		"modelCode": "model", "releaseTypeCode": "direct", "description": "Add visa route",
		"scope": map[string]any{"region": "cn", "environment": "production"},
		"items": []any{map[string]any{"action": "ADD", "after": map[string]string{"route_code": "visa", "priority": "7"}, "expectedRecordRevision": 0, "expectedCollectionRevision": 7}},
	})
	if createResponse.Code != http.StatusCreated || releases.lastCreate.Actor != "operator@example.com" || releases.lastCreate.ActorName != "Operator" || releases.lastCreate.IdempotencyKey != "create-id" {
		t.Fatalf("create response=%d command=%+v body=%s", createResponse.Code, releases.lastCreate, createResponse.Body.String())
	}

	actionResponse := serveJSON(t, handler, http.MethodPost, "/api/v1/releases/order-1/actions", "action-id", map[string]any{
		"action": "EXECUTE", "expectedOrderRevision": 1, "expectedCurrentStep": "base-apply",
	})
	if actionResponse.Code != http.StatusOK || releases.lastAct.OrderID != "order-1" || releases.lastAct.ActionRequestID != "action-id" || releases.lastAct.Action != application.ActionExecute {
		t.Fatalf("action response=%d command=%+v body=%s", actionResponse.Code, releases.lastAct, actionResponse.Body.String())
	}

	directWrite := serveJSON(t, handler, http.MethodPost, "/api/v1/configuration-records", "", map[string]any{"data": map[string]string{"code": "bypass"}})
	if directWrite.Code != http.StatusNotFound {
		t.Fatalf("direct record route returned %d", directWrite.Code)
	}
}

func TestBFFRequiresAuthenticationAndStrictJSON(t *testing.T) {
	t.Parallel()

	handler, err := adminbff.New(&queryStub{}, &releaseStub{}, authenticator{reject: true})
	if err != nil {
		t.Fatal(err)
	}
	response := serveJSON(t, handler, http.MethodPost, "/api/v1/query-page", "", map[string]any{"modelCode": "model"})
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated response = %d", response.Code)
	}

	handler, _ = adminbff.New(&queryStub{}, &releaseStub{}, authenticator{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/query-page", bytes.NewBufferString(`{"modelCode":"model","unknown":true}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown JSON field response = %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestBFFMapsInvalidPageQueryToBadRequest(t *testing.T) {
	t.Parallel()
	handler, err := adminbff.New(&queryStub{err: pagequery.ErrInvalidArgument}, &releaseStub{}, authenticator{})
	if err != nil {
		t.Fatal(err)
	}
	response := serveJSON(t, handler, http.MethodPost, "/api/v1/query-page", "", map[string]any{
		"modelCode": "model", "scope": map[string]any{"region": "cn", "environment": "production"}, "queryType": "ONLY_DATA",
		"conditions": []any{map[string]any{"field": "unknown", "operator": "EXACT", "value": "x"}},
	})
	if response.Code != http.StatusBadRequest || !bytes.Contains(response.Body.Bytes(), []byte(`"code":"INVALID_ARGUMENT"`)) {
		t.Fatalf("invalid query response = %d %s", response.Code, response.Body.String())
	}
}

func TestBFFRevealsSensitiveFieldWithNoStoreAndTrustedPrincipal(t *testing.T) {
	t.Parallel()
	expiresAt := time.Date(2026, 8, 20, 0, 1, 0, 0, time.UTC)
	sensitive := &sensitiveStub{result: access.RevealResult{Value: "secret", ExpiresAt: expiresAt}}
	handler, err := adminbff.New(&queryStub{}, &releaseStub{}, authenticator{roles: []string{access.SensitiveViewerRole}}, sensitive)
	if err != nil {
		t.Fatal(err)
	}
	response := serveJSONWithHeaders(t, handler, http.MethodPost, "/api/v1/sensitive-fields/reveal", map[string]string{"X-Request-ID": "reveal-1"}, map[string]any{
		"modelCode": "model", "scope": map[string]any{"region": "cn", "environment": "production"},
		"recordKey": "record", "fieldName": "secret", "expectedRecordRevision": 8,
		"expectedCollectionRevision": 8, "expectedModelRevision": 7, "expectedServerEpoch": "epoch",
		"expectedSnapshotInstance": "instance", "expectedSnapshotGeneration": 1, "reason": "incident",
	})
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || !bytes.Contains(response.Body.Bytes(), []byte(`"value":"secret"`)) {
		t.Fatalf("reveal response = %d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	if sensitive.last.Principal.Subject != "operator@example.com" || sensitive.last.Principal.DisplayName != "Operator" || sensitive.last.RequestID != "reveal-1" || sensitive.last.ExpectedRecordRevision != 8 {
		t.Fatalf("reveal command = %+v", sensitive.last)
	}
}

func TestBFFReturnsStableIdempotencyError(t *testing.T) {
	t.Parallel()
	handler, err := adminbff.New(&queryStub{}, &releaseStub{actErr: release.ErrIdempotencyKeyReused}, authenticator{})
	if err != nil {
		t.Fatal(err)
	}
	response := serveJSON(t, handler, http.MethodPost, "/api/v1/releases/order/actions", "same-id", map[string]any{
		"action": "EXECUTE", "expectedOrderRevision": 1, "expectedCurrentStep": "base-apply",
	})
	if response.Code != http.StatusConflict || !bytes.Contains(response.Body.Bytes(), []byte(`"code":"IDEMPOTENCY_KEY_REUSED"`)) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestBFFMapsManualApprovalRolesAndServerCapabilities(t *testing.T) {
	t.Parallel()
	releases := &releaseStub{acted: application.OrderView{
		ID: "order", Status: release.OrderInProgress, CurrentStepCode: "review", CurrentStep: release.StepManualReview,
		CurrentStepStatus: release.StepExecuting, Revision: 2, CanApprove: true, CanReject: true,
		Steps: []application.StepView{
			{Code: "review", Type: release.StepManualReview, Status: release.StepExecuting},
			{Code: "apply", Type: release.StepBaseApply, Status: release.StepPending},
			{Code: "done", Type: release.StepComplete, Status: release.StepPending},
		},
	}}
	handler, err := adminbff.New(&queryStub{}, releases, authenticator{roles: []string{"RELEASE_APPROVER"}})
	if err != nil {
		t.Fatal(err)
	}
	response := serveJSON(t, handler, http.MethodPost, "/api/v1/releases/order/actions", "approval-id", map[string]any{
		"action": "APPROVE", "expectedOrderRevision": 2, "expectedCurrentStep": "review", "comment": "reviewed",
	})
	if response.Code != http.StatusOK || releases.lastAct.Action != application.ActionApprove || releases.lastAct.Comment != "reviewed" || len(releases.lastAct.Roles) != 1 || releases.lastAct.Roles[0] != "RELEASE_APPROVER" {
		t.Fatalf("response=%d command=%+v body=%s", response.Code, releases.lastAct, response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"allowedActions":["APPROVE","REJECT"]`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"currentStep":"review"`)) {
		t.Fatalf("manual detail = %s", response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`{"code":"apply","status":"PENDING","type":"BASE_APPLY"}`)) {
		t.Fatalf("template step list = %s", response.Body.String())
	}
}

func TestBFFMapsCompareMismatchToPreconditionFailed(t *testing.T) {
	t.Parallel()
	releases := &releaseStub{actErr: release.ErrFailedPrecondition}
	handler, err := adminbff.New(&queryStub{}, releases, authenticator{})
	if err != nil {
		t.Fatal(err)
	}
	response := serveJSON(t, handler, http.MethodPost, "/api/v1/releases/order/actions", "compare-id", map[string]any{
		"action": "EXECUTE", "expectedOrderRevision": 3, "expectedCurrentStep": "compare",
	})
	if response.Code != http.StatusPreconditionFailed || !bytes.Contains(response.Body.Bytes(), []byte(`"code":"COMPARE_MISMATCH"`)) {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
}

func TestBFFMapsOverlayReleaseAndRollbackCapability(t *testing.T) {
	t.Parallel()
	releases := &releaseStub{
		created: application.OrderView{ID: "overlay-order", Status: release.OrderInProgress, CurrentStepCode: "apply-overlay", CurrentStep: release.StepOverlayApply, CurrentStepStatus: release.StepPending, Revision: 1, CanExecute: true},
		acted:   application.OrderView{ID: "overlay-order", Status: release.OrderInProgress, CurrentStepCode: "apply-overlay", CurrentStep: release.StepOverlayApply, CurrentStepStatus: release.StepExecuted, Revision: 2, CanRollback: true},
	}
	handler, err := adminbff.New(&queryStub{}, releases, authenticator{})
	if err != nil {
		t.Fatal(err)
	}
	before := map[string]string{"route_code": "visa", "priority": "1", "enabled": "false"}
	created := serveJSON(t, handler, http.MethodPost, "/api/v1/releases", "overlay-create", map[string]any{
		"modelCode": "model", "releaseTypeCode": "scope", "description": "Blue priority",
		"scope": map[string]any{"region": "cn", "environment": "production", "stage": "blue"},
		"items": []any{map[string]any{
			"action": "MODIFY", "baseBefore": before, "effectiveBefore": before,
			"after":                  map[string]string{"route_code": "visa", "priority": "2", "enabled": "false"},
			"expectedRecordRevision": 5, "expectedCollectionRevision": 7,
		}},
	})
	if created.Code != http.StatusCreated || releases.lastCreate.Items[0].Action != release.ChangeModify || releases.lastCreate.Items[0].EffectiveBefore["priority"] != "1" {
		t.Fatalf("overlay create=%d command=%+v body=%s", created.Code, releases.lastCreate, created.Body.String())
	}
	rolledBack := serveJSON(t, handler, http.MethodPost, "/api/v1/releases/overlay-order/actions", "overlay-rollback", map[string]any{
		"action": "ROLLBACK", "expectedOrderRevision": 2, "expectedCurrentStep": "apply-overlay",
	})
	if rolledBack.Code != http.StatusOK || releases.lastAct.Action != application.ActionRollback || !bytes.Contains(rolledBack.Body.Bytes(), []byte(`"allowedActions":["ROLLBACK"]`)) {
		t.Fatalf("rollback=%d command=%+v body=%s", rolledBack.Code, releases.lastAct, rolledBack.Body.String())
	}
}

func TestBFFCreatesLinkedCompensation(t *testing.T) {
	t.Parallel()
	releases := &releaseStub{compensated: application.OrderView{
		ID: "compensation", Description: "restore", CompensatesOrderID: "source", Status: release.OrderInProgress,
		CurrentStepCode: "review", CurrentStep: release.StepManualReview, CurrentStepStatus: release.StepPending, Revision: 1, CanExecute: true,
	}}
	handler, err := adminbff.New(&queryStub{}, releases, authenticator{})
	if err != nil {
		t.Fatal(err)
	}
	response := serveJSON(t, handler, http.MethodPost, "/api/v1/releases/source/compensations", "compensation-id", map[string]string{"description": "restore"})
	if response.Code != http.StatusCreated || releases.lastCompensate.OrderID != "source" || releases.lastCompensate.ActorName != "Operator" || !bytes.Contains(response.Body.Bytes(), []byte(`"compensatesOrderId":"source"`)) {
		t.Fatalf("compensation=%d command=%+v body=%s", response.Code, releases.lastCompensate, response.Body.String())
	}
}

func TestBFFListsSafeOutboxMetadataAndMapsReplay(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	operations := &outboxStub{page: outbox.EventPage{
		Events: []outbox.Event{{
			ID: "event", Sequence: 12, Type: "CONFIGURATION_CHANGED", Payload: []byte(`{"secret":"must-not-leak"}`),
			Status: outbox.StatusDeadLetter, LeaseRevision: 6, Attempts: 20, NextAttemptAt: now, LastError: "delivery failed",
		}},
		PageNumber: 1, PageSize: 20, TotalNumber: 1, TotalPages: 1,
	}, replayed: outbox.Event{ID: "event", Status: outbox.StatusPending, LeaseRevision: 7, NextAttemptAt: now}}
	diagnostics := diagnosticsStub{value: snapshot.Diagnostics{
		Identity:    snapshot.Identity{ServerEpoch: "epoch", ServerInstanceID: "server", SnapshotInstance: "instance", Generation: 3, PublishedAt: now},
		Environment: "production", Collections: []snapshot.CollectionDiagnostic{{Name: "payment_routes", Revision: 8, Digest: catalog.Digest{Algorithm: "SHA-256", Value: "digest"}}},
	}}
	handler, err := adminbff.NewWithOperations(&queryStub{}, &releaseStub{}, authenticator{roles: []string{outbox.PlatformOperatorRole}}, operations, diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/outbox-events?status=DEAD_LETTER&page=1&size=20", nil)
	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, request)
	if listed.Code != http.StatusOK || operations.lastList.Status == nil || *operations.lastList.Status != outbox.StatusDeadLetter || operations.lastPrincipal.Subject != "operator@example.com" || !bytes.Contains(listed.Body.Bytes(), []byte(`"leaseRevision":6`)) || bytes.Contains(listed.Body.Bytes(), []byte("must-not-leak")) {
		t.Fatalf("list=%d principal=%+v request=%+v body=%s", listed.Code, operations.lastPrincipal, operations.lastList, listed.Body.String())
	}
	replayed := serveJSON(t, handler, http.MethodPost, "/api/v1/outbox-events/event/replay", "", map[string]any{
		"expectedEventRevision": 6, "reason": "downstream recovered", "confirmation": "REPLAY event",
	})
	if replayed.Code != http.StatusOK || operations.lastReplay.ExpectedRevision != 6 || operations.lastReplay.Principal.Roles[0] != outbox.PlatformOperatorRole || !bytes.Contains(replayed.Body.Bytes(), []byte(`"status":"PENDING"`)) {
		t.Fatalf("replay=%d command=%+v body=%s", replayed.Code, operations.lastReplay, replayed.Body.String())
	}
	snapshotRequest := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/snapshot", nil)
	snapshotResponse := httptest.NewRecorder()
	handler.ServeHTTP(snapshotResponse, snapshotRequest)
	if snapshotResponse.Code != http.StatusOK || !bytes.Contains(snapshotResponse.Body.Bytes(), []byte(`"generation":3`)) || !bytes.Contains(snapshotResponse.Body.Bytes(), []byte(`"revision":8`)) || bytes.Contains(snapshotResponse.Body.Bytes(), []byte("must-not-leak")) {
		t.Fatalf("snapshot diagnostics=%d %s", snapshotResponse.Code, snapshotResponse.Body.String())
	}
}

type authenticator struct {
	reject bool
	roles  []string
}

func (authenticator authenticator) Authenticate(*http.Request) (adminbff.Principal, error) {
	if authenticator.reject {
		return adminbff.Principal{}, adminbff.ErrUnauthenticated
	}
	return adminbff.Principal{Subject: "operator@example.com", DisplayName: "Operator", Roles: append([]string(nil), authenticator.roles...)}, nil
}

type queryStub struct {
	result pagequery.Result
	last   pagequery.Request
	err    error
}

func (stub *queryStub) Query(request pagequery.Request) (pagequery.Result, error) {
	stub.last = request
	return stub.result, stub.err
}

type releaseStub struct {
	created        application.OrderView
	acted          application.OrderView
	compensated    application.OrderView
	lastCreate     application.CreateReleaseCommand
	lastAct        application.ActCommand
	lastCompensate application.CreateCompensatingReleaseCommand
	actErr         error
}

func (stub *releaseStub) CreateCompensatingRelease(_ context.Context, command application.CreateCompensatingReleaseCommand) (application.OrderView, error) {
	stub.lastCompensate = command
	return stub.compensated, nil
}

type sensitiveStub struct {
	result access.RevealResult
	err    error
	last   access.RevealCommand
}

type outboxStub struct {
	page          outbox.EventPage
	replayed      outbox.Event
	lastPrincipal outbox.Principal
	lastList      outbox.ListRequest
	lastReplay    outbox.ReplayCommand
}

type diagnosticsStub struct{ value snapshot.Diagnostics }

func (stub diagnosticsStub) Diagnostics() snapshot.Diagnostics { return stub.value }

func (stub *outboxStub) List(_ context.Context, principal outbox.Principal, request outbox.ListRequest) (outbox.EventPage, error) {
	stub.lastPrincipal, stub.lastList = principal, request
	return stub.page, nil
}

func (stub *outboxStub) Replay(_ context.Context, command outbox.ReplayCommand) (outbox.Event, error) {
	stub.lastReplay = command
	return stub.replayed, nil
}

func (stub *sensitiveStub) Reveal(_ context.Context, command access.RevealCommand) (access.RevealResult, error) {
	stub.last = command
	return stub.result, stub.err
}

func (stub *releaseStub) CreateRelease(_ context.Context, command application.CreateReleaseCommand) (application.OrderView, error) {
	stub.lastCreate = command
	return stub.created, nil
}

func (stub *releaseStub) Act(_ context.Context, command application.ActCommand) (application.OrderView, error) {
	stub.lastAct = command
	return stub.acted, stub.actErr
}

func serveJSON(t *testing.T, handler http.Handler, method, path, idempotency string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	if idempotency != "" {
		request.Header.Set("Idempotency-Key", idempotency)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func serveJSONWithHeaders(t *testing.T, handler http.Handler, method, path string, headers map[string]string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}
