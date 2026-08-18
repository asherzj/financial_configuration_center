package adminbff

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	"github.com/asherzj/financial_configuration_center/internal/pagequery"
	"github.com/asherzj/financial_configuration_center/internal/release/application"
	release "github.com/asherzj/financial_configuration_center/internal/release/domain"
)

var ErrUnauthenticated = errors.New("unauthenticated")

type Principal struct{ Subject string }

type Authenticator interface {
	Authenticate(*http.Request) (Principal, error)
}

type PageQueries interface {
	Query(pagequery.Request) (pagequery.Result, error)
}

type ReleaseCommands interface {
	CreateBaseFinal(context.Context, application.CreateBaseFinalCommand) (application.OrderView, error)
	Act(context.Context, application.ActCommand) (application.OrderView, error)
}

type Handler struct {
	queries  PageQueries
	releases ReleaseCommands
	auth     Authenticator
	mux      *http.ServeMux
}

func New(queries PageQueries, releases ReleaseCommands, auth Authenticator) (*Handler, error) {
	if queries == nil || releases == nil || auth == nil {
		return nil, errors.New("new Admin BFF: queries, releases, and authenticator are required")
	}
	handler := &Handler{queries: queries, releases: releases, auth: auth, mux: http.NewServeMux()}
	handler.mux.HandleFunc("POST /api/v1/query-page", handler.queryPage)
	handler.mux.HandleFunc("POST /api/v1/releases", handler.createRelease)
	handler.mux.HandleFunc("POST /api/v1/releases/{id}/actions", handler.actOnRelease)
	return handler, nil
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	handler.mux.ServeHTTP(writer, request)
}

type scopeRequest struct {
	Region      string `json:"region"`
	Environment string `json:"environment"`
	Stage       string `json:"stage,omitempty"`
}

type queryPageRequest struct {
	ModelCode     string       `json:"modelCode"`
	Scope         scopeRequest `json:"scope"`
	QueryType     string       `json:"queryType"`
	PageNumber    *int32       `json:"pageNumber,omitempty"`
	PageSize      *int32       `json:"pageSize,omitempty"`
	Conditions    []any        `json:"conditions,omitempty"`
	PreviewBucket *int32       `json:"previewBucket,omitempty"`
}

func (handler *Handler) queryPage(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticate(writer, request); !ok {
		return
	}
	var body queryPageRequest
	if !decodeJSON(writer, request, &body) {
		return
	}
	if len(body.Conditions) != 0 || body.PreviewBucket != nil {
		writeError(writer, http.StatusNotImplemented, "NOT_IMPLEMENTED", "filters and preview are not implemented in the base-only slice")
		return
	}
	queryType := pagequery.QueryType(body.QueryType)
	result, err := handler.queries.Query(pagequery.Request{
		ModelCode: body.ModelCode, Environment: body.Scope.Environment, Type: queryType,
		Page: pagequery.PageSpec{Number: body.PageNumber, Size: body.PageSize},
	})
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, queryPageResponse(result))
}

type releaseItemRequest struct {
	Action                     string                 `json:"action"`
	BaseBefore                 map[string]string      `json:"baseBefore,omitempty"`
	EffectiveBefore            map[string]string      `json:"effectiveBefore,omitempty"`
	After                      map[string]string      `json:"after"`
	ExpectedRecordRevision     catalog.ConfigRevision `json:"expectedRecordRevision"`
	ExpectedCollectionRevision catalog.ConfigRevision `json:"expectedCollectionRevision"`
	PreserveSensitiveFields    []string               `json:"preserveSensitiveFields,omitempty"`
}

type createReleaseRequest struct {
	ModelCode       string               `json:"modelCode"`
	ReleaseTypeCode string               `json:"releaseTypeCode"`
	Scope           scopeRequest         `json:"scope"`
	Description     string               `json:"description"`
	Items           []releaseItemRequest `json:"items"`
}

func (handler *Handler) createRelease(writer http.ResponseWriter, request *http.Request) {
	principal, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	idempotency := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if idempotency == "" {
		writeError(writer, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key header is required")
		return
	}
	var body createReleaseRequest
	if !decodeJSON(writer, request, &body) {
		return
	}
	if body.ReleaseTypeCode != "direct" || strings.TrimSpace(body.Description) == "" || len(body.Items) == 0 {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "direct release type, description, and items are required")
		return
	}
	drafts := make([]application.AddDraft, len(body.Items))
	for index, item := range body.Items {
		if item.Action != "ADD" || item.After == nil {
			writeError(writer, http.StatusNotImplemented, "NOT_IMPLEMENTED", "the base-only slice accepts ADD items only")
			return
		}
		drafts[index] = application.AddDraft{Data: item.After, ExpectedRecordRevision: item.ExpectedRecordRevision, ExpectedCollectionRevision: item.ExpectedCollectionRevision}
	}
	result, err := handler.releases.CreateBaseFinal(request.Context(), application.CreateBaseFinalCommand{
		IdempotencyKey: idempotency, ModelCode: body.ModelCode,
		Scope: release.Scope{Region: body.Scope.Region, Environment: body.Scope.Environment, Stage: body.Scope.Stage},
		Actor: principal.Subject, Items: drafts,
	})
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, releaseDetail(result))
}

type releaseActionRequest struct {
	Action                string                 `json:"action"`
	ExpectedOrderRevision release.EntityRevision `json:"expectedOrderRevision"`
	ExpectedCurrentStep   string                 `json:"expectedCurrentStep"`
	Comment               string                 `json:"comment,omitempty"`
}

func (handler *Handler) actOnRelease(writer http.ResponseWriter, request *http.Request) {
	principal, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	actionID := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if actionID == "" {
		writeError(writer, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key header is required")
		return
	}
	var body releaseActionRequest
	if !decodeJSON(writer, request, &body) {
		return
	}
	var action application.Action
	switch body.Action {
	case "EXECUTE":
		action = application.ActionExecute
	case "ADVANCE":
		action = application.ActionAdvance
	default:
		writeError(writer, http.StatusNotImplemented, "NOT_IMPLEMENTED", "only EXECUTE and ADVANCE are implemented in the base-only slice")
		return
	}
	result, err := handler.releases.Act(request.Context(), application.ActCommand{
		OrderID: request.PathValue("id"), ActionRequestID: actionID,
		ExpectedRevision: body.ExpectedOrderRevision, ExpectedCurrentStep: release.StepType(body.ExpectedCurrentStep), Action: action, Actor: principal.Subject,
	})
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, releaseDetail(result))
}

func (handler *Handler) authenticate(writer http.ResponseWriter, request *http.Request) (Principal, bool) {
	principal, err := handler.auth.Authenticate(request)
	if err != nil || strings.TrimSpace(principal.Subject) == "" {
		writeError(writer, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication is required")
		return Principal{}, false
	}
	return principal, true
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, target any) bool {
	if !strings.HasPrefix(strings.ToLower(request.Header.Get("Content-Type")), "application/json") {
		writeError(writer, http.StatusUnsupportedMediaType, "CONTENT_TYPE_REQUIRED", "Content-Type must be application/json")
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_JSON", "request body is invalid")
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeError(writer, http.StatusBadRequest, "INVALID_JSON", "request body must contain exactly one JSON value")
		return false
	}
	return true
}

func writeDomainError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, release.ErrAborted):
		writeError(writer, http.StatusConflict, "ABORTED", err.Error())
	case errors.Is(err, release.ErrInvalid):
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
	default:
		writeError(writer, http.StatusInternalServerError, "INTERNAL", "request failed")
	}
}

func writeError(writer http.ResponseWriter, statusCode int, code, message string) {
	writeJSON(writer, statusCode, map[string]any{"code": code, "message": message, "traceId": ""})
}

func writeJSON(writer http.ResponseWriter, statusCode int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(statusCode)
	_ = json.NewEncoder(writer).Encode(value)
}

func releaseDetail(view application.OrderView) map[string]any {
	allowed := []string{}
	if view.Status == release.OrderInProgress {
		if view.CurrentStep == release.StepBaseApply || view.CurrentStep == release.StepComplete {
			allowed = append(allowed, "EXECUTE")
		}
		if view.CurrentStep == release.StepBaseApply && view.Revision > 1 {
			allowed = []string{"ADVANCE"}
		}
	}
	return map[string]any{
		"order": map[string]any{"id": view.ID, "status": view.Status, "currentStep": view.CurrentStep, "entityRevision": view.Revision},
		"items": []any{}, "steps": []any{map[string]any{"type": view.CurrentStep}}, "allowedActions": allowed,
	}
}

func queryPageResponse(result pagequery.Result) map[string]any {
	rows := make([]map[string]any, len(result.Rows))
	for index, row := range result.Rows {
		rows[index] = map[string]any{"recordKey": row.RecordKey, "recordRevision": row.RecordRevision, "values": row.Values, "maskedFields": row.MaskedFields}
	}
	fields := make([]map[string]any, len(result.InteractionFields))
	for index, field := range result.InteractionFields {
		fields[index] = map[string]any{
			"name": field.Name, "displayName": field.DisplayName, "description": field.Description,
			"type": field.Type, "uiControl": field.UIControl, "queryable": field.Queryable,
			"editable": field.Editable, "required": field.Required, "sensitive": field.Sensitive,
			"projected": field.Projected, "keyField": field.KeyField,
			"allowedFilterOperators": field.AllowedFilterOperators, "defaultFilterOperator": field.DefaultFilterOperator,
			"defaultValue": field.DefaultValue, "displayOrder": field.DisplayOrder, "validationRules": []any{}, "options": []any{},
		}
	}
	return map[string]any{
		"modelCode": result.ModelCode, "modelName": result.ModelName, "queryType": result.QueryType,
		"rows": rows, "projectionFields": result.ProjectionFields, "interactionFields": fields,
		"releaseTypes": []any{},
		"page":         map[string]any{"number": result.PageNumber, "size": result.PageSize, "totalNumber": result.TotalNumber, "totalPages": result.TotalPages},
		"snapshot": map[string]any{
			"serverEpoch": result.Snapshot.ServerEpoch, "serverInstanceId": result.Snapshot.ServerInstanceID,
			"snapshotInstance": result.Snapshot.SnapshotInstance, "snapshotGeneration": result.Snapshot.Generation,
			"publishedAt": result.Snapshot.PublishedAt,
		},
		"modelRevision": result.ModelRevision, "collectionRevision": result.CollectionRevision,
	}
}
