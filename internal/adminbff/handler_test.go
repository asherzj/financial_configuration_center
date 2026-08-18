package adminbff_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/asherzj/financial_configuration_center/internal/adminbff"
	"github.com/asherzj/financial_configuration_center/internal/pagequery"
	"github.com/asherzj/financial_configuration_center/internal/release/application"
	release "github.com/asherzj/financial_configuration_center/internal/release/domain"
)

func TestBrowserBaseReleaseJourneyRoutes(t *testing.T) {
	t.Parallel()

	queries := &queryStub{result: pagequery.Result{ModelCode: "model", ModelName: "Model", QueryType: pagequery.TypeAll, PageNumber: 1, PageSize: 20, ReleaseTypes: []pagequery.ReleaseType{{Code: "direct", Name: "Direct", TemplateCode: "base-final", Available: true}}}}
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
	})
	if queryResponse.Code != http.StatusOK || queries.last.Region != "cn" || queries.last.Environment != "production" || queries.last.Type != pagequery.TypeAll {
		t.Fatalf("query response=%d command=%+v body=%s", queryResponse.Code, queries.last, queryResponse.Body.String())
	}
	if !bytes.Contains(queryResponse.Body.Bytes(), []byte(`"releaseTypes":[{"available":true,"code":"direct","name":"Direct","templateCode":"base-final"}]`)) {
		t.Fatalf("release type metadata = %s", queryResponse.Body.String())
	}

	createResponse := serveJSON(t, handler, http.MethodPost, "/api/v1/releases", "create-id", map[string]any{
		"modelCode": "model", "releaseTypeCode": "direct", "description": "Add visa route",
		"scope": map[string]any{"region": "cn", "environment": "production"},
		"items": []any{map[string]any{"action": "ADD", "after": map[string]string{"route_code": "visa", "priority": "7"}, "expectedRecordRevision": 0, "expectedCollectionRevision": 7}},
	})
	if createResponse.Code != http.StatusCreated || releases.lastCreate.Actor != "operator@example.com" || releases.lastCreate.IdempotencyKey != "create-id" {
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

type authenticator struct {
	reject bool
	roles  []string
}

func (authenticator authenticator) Authenticate(*http.Request) (adminbff.Principal, error) {
	if authenticator.reject {
		return adminbff.Principal{}, adminbff.ErrUnauthenticated
	}
	return adminbff.Principal{Subject: "operator@example.com", Roles: append([]string(nil), authenticator.roles...)}, nil
}

type queryStub struct {
	result pagequery.Result
	last   pagequery.Request
}

func (stub *queryStub) Query(request pagequery.Request) (pagequery.Result, error) {
	stub.last = request
	return stub.result, nil
}

type releaseStub struct {
	created    application.OrderView
	acted      application.OrderView
	lastCreate application.CreateReleaseCommand
	lastAct    application.ActCommand
	actErr     error
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
