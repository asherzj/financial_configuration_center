package adminbff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	bffapp "github.com/asherzj/financial_configuration_center/internal/adminbff/application"
	"github.com/asherzj/financial_configuration_center/internal/audit"
	catalogapp "github.com/asherzj/financial_configuration_center/internal/catalog/application"
	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	"github.com/asherzj/financial_configuration_center/internal/outbox"
	platformauth "github.com/asherzj/financial_configuration_center/internal/platform/auth"
	"github.com/asherzj/financial_configuration_center/internal/release/application"
	release "github.com/asherzj/financial_configuration_center/internal/release/domain"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

var ErrUnauthenticated = errors.New("unauthenticated")

type Principal struct {
	Subject       string
	DisplayName   string
	Roles         []string
	AllowedScopes []platformauth.ScopePattern
}

type Authenticator interface {
	Authenticate(*http.Request) (Principal, error)
}

type ReleaseCommands interface {
	CreateRelease(context.Context, application.CreateReleaseCommand) (application.OrderView, error)
	CreateCompensatingRelease(context.Context, application.CreateCompensatingReleaseCommand) (application.OrderView, error)
	Act(context.Context, application.ActCommand) (application.OrderView, error)
}

type OutboxOperations interface {
	List(context.Context, outbox.Principal, outbox.ListRequest) (outbox.EventPage, error)
	Replay(context.Context, outbox.ReplayCommand) (outbox.Event, error)
}

type AuditQueries interface {
	List(context.Context, audit.Principal, audit.Query) (audit.Page, error)
}

type CatalogAdmin interface {
	CreateCollection(context.Context, catalogapp.Principal, catalogapp.CollectionInput) (catalogapp.CollectionView, error)
	UpdateCollection(context.Context, catalogapp.Principal, catalog.ConfigRevision, catalogapp.CollectionInput) (catalogapp.CollectionView, error)
	GetCollection(context.Context, catalogapp.Principal, string) (catalogapp.CollectionView, error)
	ListCollections(context.Context, catalogapp.Principal, catalogapp.PageQuery) (catalogapp.CollectionPage, error)
	CreateSubscription(context.Context, catalogapp.Principal, catalogapp.SubscriptionInput) (catalogapp.SubscriptionView, error)
	UpdateSubscription(context.Context, catalogapp.Principal, catalog.ConfigRevision, catalogapp.SubscriptionInput) (catalogapp.SubscriptionView, error)
	ListSubscriptions(context.Context, catalogapp.Principal, catalogapp.SubscriptionQuery) (catalogapp.SubscriptionPage, error)
	PreviewModel(context.Context, catalogapp.Principal, catalogapp.ModelInput) (catalogapp.ModelPreview, error)
	CreateModel(context.Context, catalogapp.Principal, catalogapp.ModelInput) (catalogapp.ModelView, error)
	UpdateModel(context.Context, catalogapp.Principal, catalog.ConfigRevision, catalogapp.ModelInput) (catalogapp.ModelView, error)
	GetModel(context.Context, catalogapp.Principal, string) (catalogapp.ModelView, error)
	ListModels(context.Context, catalogapp.Principal, catalogapp.ModelQuery) (catalogapp.ModelPage, error)
	CreateTemplate(context.Context, catalogapp.Principal, catalogapp.TemplateInput) (catalogapp.TemplateView, error)
	GetTemplate(context.Context, catalogapp.Principal, string, int64) (catalogapp.TemplateView, error)
	ListTemplates(context.Context, catalogapp.Principal, catalogapp.TemplateQuery) (catalogapp.TemplatePage, error)
}

type Handler struct {
	queries     bffapp.PageQueryPort
	releases    ReleaseCommands
	sensitive   bffapp.SensitiveAccessUseCase
	outbox      OutboxOperations
	diagnostics bffapp.DiagnosticsUseCase
	audits      AuditQueries
	catalog     CatalogAdmin
	auth        Authenticator
	mux         *http.ServeMux
}

func NewWithOutbox(queries bffapp.PageQueryPort, releases ReleaseCommands, auth Authenticator, operations OutboxOperations, sensitive ...bffapp.SensitiveAccessUseCase) (*Handler, error) {
	return newWithOperations(queries, releases, auth, operations, nil, nil, nil, sensitive...)
}

func NewWithOperations(queries bffapp.PageQueryPort, releases ReleaseCommands, auth Authenticator, operations OutboxOperations, diagnostics bffapp.DiagnosticsUseCase, sensitive ...bffapp.SensitiveAccessUseCase) (*Handler, error) {
	if diagnostics == nil || isNilDependency(diagnostics) {
		return nil, errors.New("new Admin BFF: snapshot diagnostics are required")
	}
	return newWithOperations(queries, releases, auth, operations, diagnostics, nil, nil, sensitive...)
}

func NewWithAdminOperations(queries bffapp.PageQueryPort, releases ReleaseCommands, auth Authenticator, operations OutboxOperations, diagnostics bffapp.DiagnosticsUseCase, audits AuditQueries, sensitive ...bffapp.SensitiveAccessUseCase) (*Handler, error) {
	if diagnostics == nil || isNilDependency(diagnostics) || audits == nil {
		return nil, errors.New("new Admin BFF: snapshot diagnostics and audit queries are required")
	}
	return newWithOperations(queries, releases, auth, operations, diagnostics, audits, nil, sensitive...)
}

func NewWithCatalogOperations(queries bffapp.PageQueryPort, releases ReleaseCommands, auth Authenticator, operations OutboxOperations, diagnostics bffapp.DiagnosticsUseCase, audits AuditQueries, catalogAdmin CatalogAdmin, sensitive ...bffapp.SensitiveAccessUseCase) (*Handler, error) {
	if diagnostics == nil || isNilDependency(diagnostics) || audits == nil || catalogAdmin == nil {
		return nil, errors.New("new Admin BFF: diagnostics, audit queries, and catalog admin are required")
	}
	return newWithOperations(queries, releases, auth, operations, diagnostics, audits, catalogAdmin, sensitive...)
}

func newWithOperations(queries bffapp.PageQueryPort, releases ReleaseCommands, auth Authenticator, operations OutboxOperations, diagnostics bffapp.DiagnosticsUseCase, audits AuditQueries, catalogAdmin CatalogAdmin, sensitive ...bffapp.SensitiveAccessUseCase) (*Handler, error) {
	if operations == nil {
		return nil, errors.New("new Admin BFF: outbox operations are required")
	}
	handler, err := New(queries, releases, auth, sensitive...)
	if err != nil {
		return nil, err
	}
	handler.outbox = operations
	handler.diagnostics = diagnostics
	handler.audits = audits
	handler.catalog = catalogAdmin
	handler.mux.HandleFunc("GET /api/v1/outbox-events", handler.listOutboxEvents)
	handler.mux.HandleFunc("POST /api/v1/outbox-events/{id}/replay", handler.replayOutboxEvent)
	if diagnostics != nil {
		handler.mux.HandleFunc("GET /api/v1/diagnostics/snapshot", handler.getSnapshotDiagnostics)
		handler.mux.HandleFunc("GET /api/v1/diagnostics/collections/{name}", handler.getCollectionDiagnostics)
	}
	if audits != nil {
		handler.mux.HandleFunc("GET /api/v1/audit-records", handler.listAuditRecords)
	}
	if catalogAdmin != nil {
		handler.mux.HandleFunc("GET /api/v1/collections", handler.listCollections)
		handler.mux.HandleFunc("POST /api/v1/collections", handler.createCollection)
		handler.mux.HandleFunc("GET /api/v1/collections/{name}", handler.getCollection)
		handler.mux.HandleFunc("PUT /api/v1/collections/{name}", handler.updateCollection)
		handler.mux.HandleFunc("GET /api/v1/subscriptions", handler.listSubscriptions)
		handler.mux.HandleFunc("POST /api/v1/subscriptions", handler.createSubscription)
		handler.mux.HandleFunc("PUT /api/v1/subscriptions/{id}", handler.updateSubscription)
		handler.mux.HandleFunc("POST /api/v1/models/preview", handler.previewModel)
		handler.mux.HandleFunc("GET /api/v1/models", handler.listModels)
		handler.mux.HandleFunc("POST /api/v1/models", handler.createModel)
		handler.mux.HandleFunc("GET /api/v1/models/{code}", handler.getModel)
		handler.mux.HandleFunc("PUT /api/v1/models/{code}", handler.updateModel)
		handler.mux.HandleFunc("GET /api/v1/templates", handler.listTemplates)
		handler.mux.HandleFunc("POST /api/v1/templates", handler.createTemplate)
		handler.mux.HandleFunc("GET /api/v1/templates/{code}/versions/{version}", handler.getTemplate)
	}
	return handler, nil
}

type collectionAdminRequest struct {
	Name               string                        `json:"name"`
	Description        string                        `json:"description"`
	Fields             []collectionFieldAdminRequest `json:"fields"`
	KeyFields          []string                      `json:"keyFields"`
	SDKDeliveryEnabled bool                          `json:"sdkDeliveryEnabled"`
	SchemaVersion      int64                         `json:"schemaVersion"`
	Status             catalogapp.Status             `json:"status"`
}

type collectionFieldAdminRequest struct {
	Name            string                   `json:"name"`
	DisplayName     string                   `json:"displayName"`
	Type            catalog.FieldType        `json:"type"`
	Required        bool                     `json:"required"`
	Sensitive       bool                     `json:"sensitive"`
	DefaultValue    *string                  `json:"defaultValue,omitempty"`
	Description     string                   `json:"description"`
	DisplayOrder    int32                    `json:"displayOrder"`
	ValidationRules []catalog.ValidationRule `json:"validationRules"`
}

func (body collectionAdminRequest) input() catalogapp.CollectionInput {
	fields := make([]catalog.FieldDefinition, len(body.Fields))
	for index, field := range body.Fields {
		fields[index] = catalog.FieldDefinition{Name: field.Name, DisplayName: field.DisplayName, Type: field.Type, Required: field.Required, Sensitive: field.Sensitive, DefaultValue: field.DefaultValue, Description: field.Description, DisplayOrder: field.DisplayOrder, ValidationRules: field.ValidationRules}
	}
	return catalogapp.CollectionInput{Name: body.Name, Description: body.Description, Fields: fields, KeyFields: body.KeyFields, SDKDeliveryEnabled: body.SDKDeliveryEnabled, SchemaVersion: body.SchemaVersion, Status: body.Status}
}

func (handler *Handler) createCollection(writer http.ResponseWriter, request *http.Request) {
	principal, ok := handler.catalogPrincipal(writer, request)
	if !ok {
		return
	}
	var body collectionAdminRequest
	if !decodeJSON(writer, request, &body) {
		return
	}
	view, err := handler.catalog.CreateCollection(request.Context(), principal, body.input())
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, collectionAdminResponse(view))
}

func (handler *Handler) updateCollection(writer http.ResponseWriter, request *http.Request) {
	principal, ok := handler.catalogPrincipal(writer, request)
	if !ok {
		return
	}
	expected, ok := expectedRevision(writer, request)
	if !ok {
		return
	}
	var body collectionAdminRequest
	if !decodeJSON(writer, request, &body) {
		return
	}
	if body.Name != request.PathValue("name") {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "collection path and body names must match")
		return
	}
	view, err := handler.catalog.UpdateCollection(request.Context(), principal, expected, body.input())
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, collectionAdminResponse(view))
}

func (handler *Handler) getCollection(writer http.ResponseWriter, request *http.Request) {
	principal, ok := handler.catalogPrincipal(writer, request)
	if !ok {
		return
	}
	view, err := handler.catalog.GetCollection(request.Context(), principal, request.PathValue("name"))
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, collectionAdminResponse(view))
}

func (handler *Handler) listCollections(writer http.ResponseWriter, request *http.Request) {
	principal, ok := handler.catalogPrincipal(writer, request)
	if !ok {
		return
	}
	number, size, ok := queryPage(writer, request)
	if !ok {
		return
	}
	page, err := handler.catalog.ListCollections(request.Context(), principal, catalogapp.PageQuery{PageNumber: number, PageSize: size})
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	collections := make([]map[string]any, len(page.Collections))
	for index, collection := range page.Collections {
		collections[index] = collectionAdminResponse(collection)
	}
	writeJSON(writer, http.StatusOK, map[string]any{"collections": collections, "page": pageResponse(page.PageNumber, page.PageSize, page.TotalNumber, page.TotalPages)})
}

type subscriptionAdminRequest struct {
	ID          string                 `json:"id,omitempty"`
	ConsumerID  string                 `json:"consumerId"`
	Collection  string                 `json:"collection"`
	IndexName   string                 `json:"indexName"`
	IndexFields []string               `json:"indexFields"`
	Cardinality catalogapp.Cardinality `json:"cardinality"`
	Enabled     bool                   `json:"enabled"`
}

func (body subscriptionAdminRequest) input() catalogapp.SubscriptionInput {
	return catalogapp.SubscriptionInput{ID: body.ID, ConsumerID: body.ConsumerID, Collection: body.Collection, IndexName: body.IndexName, IndexFields: body.IndexFields, Cardinality: body.Cardinality, Enabled: body.Enabled}
}

func (handler *Handler) createSubscription(writer http.ResponseWriter, request *http.Request) {
	principal, ok := handler.catalogPrincipal(writer, request)
	if !ok {
		return
	}
	var body subscriptionAdminRequest
	if !decodeJSON(writer, request, &body) {
		return
	}
	view, err := handler.catalog.CreateSubscription(request.Context(), principal, body.input())
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, subscriptionAdminResponse(view))
}

func (handler *Handler) updateSubscription(writer http.ResponseWriter, request *http.Request) {
	principal, ok := handler.catalogPrincipal(writer, request)
	if !ok {
		return
	}
	expected, ok := expectedRevision(writer, request)
	if !ok {
		return
	}
	var body subscriptionAdminRequest
	if !decodeJSON(writer, request, &body) {
		return
	}
	if body.ID != "" && body.ID != request.PathValue("id") {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "subscription path and body IDs must match")
		return
	}
	body.ID = request.PathValue("id")
	view, err := handler.catalog.UpdateSubscription(request.Context(), principal, expected, body.input())
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, subscriptionAdminResponse(view))
}

func (handler *Handler) listSubscriptions(writer http.ResponseWriter, request *http.Request) {
	principal, ok := handler.catalogPrincipal(writer, request)
	if !ok {
		return
	}
	number, size, ok := queryPage(writer, request)
	if !ok {
		return
	}
	page, err := handler.catalog.ListSubscriptions(request.Context(), principal, catalogapp.SubscriptionQuery{ConsumerID: request.URL.Query().Get("consumerId"), Collection: request.URL.Query().Get("collection"), PageNumber: number, PageSize: size})
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	subscriptions := make([]map[string]any, len(page.Subscriptions))
	for index, subscription := range page.Subscriptions {
		subscriptions[index] = subscriptionAdminResponse(subscription)
	}
	writeJSON(writer, http.StatusOK, map[string]any{"subscriptions": subscriptions, "page": pageResponse(page.PageNumber, page.PageSize, page.TotalNumber, page.TotalPages)})
}

type modelAdminRequest struct {
	Code       string          `json:"code"`
	Name       string          `json:"name"`
	Collection string          `json:"collection"`
	Definition json.RawMessage `json:"definition"`
	Enabled    bool            `json:"enabled"`
}

func (body modelAdminRequest) input() catalogapp.ModelInput {
	return catalogapp.ModelInput{Code: body.Code, Name: body.Name, Collection: body.Collection, Definition: append([]byte(nil), body.Definition...), Enabled: body.Enabled}
}

func (handler *Handler) previewModel(writer http.ResponseWriter, request *http.Request) {
	principal, ok := handler.catalogPrincipal(writer, request)
	if !ok {
		return
	}
	var body modelAdminRequest
	if !decodeJSON(writer, request, &body) {
		return
	}
	preview, err := handler.catalog.PreviewModel(request.Context(), principal, body.input())
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	response := map[string]any{"valid": preview.Valid, "issues": preview.Issues}
	if preview.Valid {
		response["normalizedDefinition"] = json.RawMessage(preview.NormalizedDefinition)
	}
	writeJSON(writer, http.StatusOK, response)
}

func (handler *Handler) createModel(writer http.ResponseWriter, request *http.Request) {
	principal, ok := handler.catalogPrincipal(writer, request)
	if !ok {
		return
	}
	var body modelAdminRequest
	if !decodeJSON(writer, request, &body) {
		return
	}
	view, err := handler.catalog.CreateModel(request.Context(), principal, body.input())
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, modelAdminResponse(view))
}

func (handler *Handler) updateModel(writer http.ResponseWriter, request *http.Request) {
	principal, ok := handler.catalogPrincipal(writer, request)
	if !ok {
		return
	}
	expected, ok := expectedRevision(writer, request)
	if !ok {
		return
	}
	var body modelAdminRequest
	if !decodeJSON(writer, request, &body) {
		return
	}
	if body.Code != request.PathValue("code") {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "model path and body codes must match")
		return
	}
	view, err := handler.catalog.UpdateModel(request.Context(), principal, expected, body.input())
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, modelAdminResponse(view))
}

func (handler *Handler) getModel(writer http.ResponseWriter, request *http.Request) {
	principal, ok := handler.catalogPrincipal(writer, request)
	if !ok {
		return
	}
	view, err := handler.catalog.GetModel(request.Context(), principal, request.PathValue("code"))
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, modelAdminResponse(view))
}

func (handler *Handler) listModels(writer http.ResponseWriter, request *http.Request) {
	principal, ok := handler.catalogPrincipal(writer, request)
	if !ok {
		return
	}
	number, size, ok := queryPage(writer, request)
	if !ok {
		return
	}
	page, err := handler.catalog.ListModels(request.Context(), principal, catalogapp.ModelQuery{Collection: request.URL.Query().Get("collection"), PageNumber: number, PageSize: size})
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	models := make([]map[string]any, len(page.Models))
	for index, model := range page.Models {
		models[index] = modelAdminResponse(model)
	}
	writeJSON(writer, http.StatusOK, map[string]any{"models": models, "page": pageResponse(page.PageNumber, page.PageSize, page.TotalNumber, page.TotalPages)})
}

type templateAdminRequest struct {
	Code                     string              `json:"code"`
	Name                     string              `json:"name"`
	ModelCode                string              `json:"modelCode"`
	ReleaseTypeCode          string              `json:"releaseTypeCode"`
	FinalEffect              release.FinalEffect `json:"finalEffect"`
	SchedulingAllowed        bool                `json:"schedulingAllowed"`
	MaxScheduleWindowSeconds int64               `json:"maxScheduleWindowSeconds"`
	Document                 json.RawMessage     `json:"document"`
	AllowedRoles             []string            `json:"allowedRoles"`
	Enabled                  bool                `json:"enabled"`
}

func (body templateAdminRequest) input() catalogapp.TemplateInput {
	return catalogapp.TemplateInput{Code: body.Code, Name: body.Name, ModelCode: body.ModelCode, ReleaseTypeCode: body.ReleaseTypeCode, FinalEffect: body.FinalEffect, SchedulingAllowed: body.SchedulingAllowed, MaxScheduleWindowSeconds: body.MaxScheduleWindowSeconds, Document: append([]byte(nil), body.Document...), AllowedRoles: body.AllowedRoles, Enabled: body.Enabled}
}

func (handler *Handler) createTemplate(writer http.ResponseWriter, request *http.Request) {
	principal, ok := handler.catalogPrincipal(writer, request)
	if !ok {
		return
	}
	var body templateAdminRequest
	if !decodeJSON(writer, request, &body) {
		return
	}
	view, err := handler.catalog.CreateTemplate(request.Context(), principal, body.input())
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, templateAdminResponse(view))
}

func (handler *Handler) getTemplate(writer http.ResponseWriter, request *http.Request) {
	principal, ok := handler.catalogPrincipal(writer, request)
	if !ok {
		return
	}
	version, err := strconv.ParseInt(request.PathValue("version"), 10, 64)
	if err != nil || version <= 0 {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "template version must be positive")
		return
	}
	view, err := handler.catalog.GetTemplate(request.Context(), principal, request.PathValue("code"), version)
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, templateAdminResponse(view))
}

func (handler *Handler) listTemplates(writer http.ResponseWriter, request *http.Request) {
	principal, ok := handler.catalogPrincipal(writer, request)
	if !ok {
		return
	}
	number, size, ok := queryPage(writer, request)
	if !ok {
		return
	}
	page, err := handler.catalog.ListTemplates(request.Context(), principal, catalogapp.TemplateQuery{ModelCode: request.URL.Query().Get("modelCode"), PageNumber: number, PageSize: size})
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	templates := make([]map[string]any, len(page.Templates))
	for index, template := range page.Templates {
		templates[index] = templateAdminResponse(template)
	}
	writeJSON(writer, http.StatusOK, map[string]any{"templates": templates, "page": pageResponse(page.PageNumber, page.PageSize, page.TotalNumber, page.TotalPages)})
}

func (handler *Handler) catalogPrincipal(writer http.ResponseWriter, request *http.Request) (catalogapp.Principal, bool) {
	principal, ok := handler.authenticate(writer, request)
	return catalogapp.Principal{Subject: principal.Subject, DisplayName: principal.DisplayName, Roles: append([]string(nil), principal.Roles...)}, ok
}

func expectedRevision(writer http.ResponseWriter, request *http.Request) (catalog.ConfigRevision, bool) {
	raw := strings.TrimSpace(request.Header.Get("If-Match"))
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		writeError(writer, http.StatusBadRequest, "EXPECTED_REVISION_REQUIRED", "If-Match must contain a positive revision")
		return 0, false
	}
	return catalog.ConfigRevision(value), true
}

func queryPage(writer http.ResponseWriter, request *http.Request) (int, int, bool) {
	number, err := positiveQueryInt(request, "page", 1)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return 0, 0, false
	}
	size, err := positiveQueryInt(request, "size", 20)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return 0, 0, false
	}
	return number, size, true
}

func collectionAdminResponse(view catalogapp.CollectionView) map[string]any {
	return map[string]any{"name": view.Name, "description": view.Description, "fields": view.Fields, "keyFields": view.KeyFields, "sdkDeliveryEnabled": view.SDKDeliveryEnabled, "schemaVersion": view.SchemaVersion, "status": view.Status, "configRevision": view.ConfigRevision, "audit": map[string]any{"createdAt": view.Audit.CreatedAt, "createdBy": view.Audit.CreatedBy, "updatedAt": view.Audit.UpdatedAt, "updatedBy": view.Audit.UpdatedBy}}
}

func subscriptionAdminResponse(view catalogapp.SubscriptionView) map[string]any {
	return map[string]any{"id": view.ID, "consumerId": view.ConsumerID, "collection": view.Collection, "indexName": view.IndexName, "indexFields": view.IndexFields, "cardinality": view.Cardinality, "enabled": view.Enabled, "configRevision": view.ConfigRevision}
}

func modelAdminResponse(view catalogapp.ModelView) map[string]any {
	return map[string]any{"code": view.Code, "name": view.Name, "collection": view.Collection, "definition": json.RawMessage(view.Definition), "enabled": view.Enabled, "configRevision": view.ConfigRevision, "audit": map[string]any{"createdAt": view.Audit.CreatedAt, "createdBy": view.Audit.CreatedBy, "updatedAt": view.Audit.UpdatedAt, "updatedBy": view.Audit.UpdatedBy}}
}

func templateAdminResponse(view catalogapp.TemplateView) map[string]any {
	return map[string]any{"code": view.Code, "name": view.Name, "modelCode": view.ModelCode, "releaseTypeCode": view.ReleaseTypeCode, "version": view.Version, "finalEffect": view.FinalEffect, "schedulingAllowed": view.SchedulingAllowed, "maxScheduleWindowSeconds": view.MaxScheduleWindowSeconds, "document": json.RawMessage(view.Document), "allowedRoles": view.AllowedRoles, "enabled": view.Enabled, "audit": map[string]any{"createdAt": view.Audit.CreatedAt, "createdBy": view.Audit.CreatedBy}}
}

func pageResponse(number, size int, total int64, pages int) map[string]any {
	return map[string]any{"number": number, "size": size, "totalNumber": total, "totalPages": pages}
}

func (handler *Handler) listAuditRecords(writer http.ResponseWriter, request *http.Request) {
	principal, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	pageNumber, err := positiveQueryInt(request, "page", 1)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	pageSize, err := positiveQueryInt(request, "size", 20)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	from, err := optionalQueryTime(request, "from")
	if err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	until, err := optionalQueryTime(request, "until")
	if err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	page, err := handler.audits.List(request.Context(), audit.Principal{Subject: principal.Subject, Roles: append([]string(nil), principal.Roles...)}, audit.Query{
		PrincipalSubject: request.URL.Query().Get("principalSubject"), ResourceType: request.URL.Query().Get("resourceType"), ResourceID: request.URL.Query().Get("resourceId"),
		From: from, Until: until, PageNumber: pageNumber, PageSize: pageSize,
	})
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	records := make([]map[string]any, len(page.Records))
	for index, record := range page.Records {
		records[index] = map[string]any{
			"id": record.ID, "occurredAt": record.OccurredAt, "principalSubject": record.PrincipalSubject,
			"action": record.Action, "resourceType": record.ResourceType, "resourceId": record.ResourceID,
			"scope":  map[string]any{"region": record.Region, "environment": record.Environment, "stage": record.Stage},
			"result": record.Result, "traceId": record.TraceID,
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"records": records,
		"page":    map[string]any{"number": page.PageNumber, "size": page.PageSize, "totalNumber": page.TotalNumber, "totalPages": page.TotalPages},
	})
}

func (handler *Handler) getSnapshotDiagnostics(writer http.ResponseWriter, request *http.Request) {
	principal, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	diagnostics, err := handler.diagnostics.SnapshotDiagnostics(request.Context(), diagnosticPrincipal(principal))
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, snapshotDiagnosticsResponse(diagnostics))
}

func (handler *Handler) getCollectionDiagnostics(writer http.ResponseWriter, request *http.Request) {
	principal, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	name := request.PathValue("name")
	diagnostics, err := handler.diagnostics.CollectionDiagnostics(request.Context(), diagnosticPrincipal(principal), name)
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"collection": diagnostics.Name, "environment": diagnostics.Environment,
		"revision": diagnostics.Revision, "digest": diagnostics.Digest,
		"lastErrorCode": diagnostics.LastErrorCode,
	})
}

func diagnosticPrincipal(principal Principal) bffapp.DiagnosticPrincipal {
	return bffapp.DiagnosticPrincipal{
		Subject: principal.Subject, Roles: append([]string(nil), principal.Roles...),
		AllowedScopes: append([]platformauth.ScopePattern(nil), principal.AllowedScopes...),
	}
}

func snapshotDiagnosticsResponse(diagnostics bffapp.SnapshotDiagnostics) map[string]any {
	collections := make([]map[string]any, len(diagnostics.Collections))
	for index, collection := range diagnostics.Collections {
		collections[index] = map[string]any{"name": collection.Name, "revision": collection.Revision, "digest": collection.Digest}
	}
	return map[string]any{
		"snapshot": map[string]any{
			"serverEpoch": diagnostics.Identity.ServerEpoch, "serverInstanceId": diagnostics.Identity.ServerInstanceID,
			"snapshotInstance": diagnostics.Identity.SnapshotInstance, "generation": diagnostics.Identity.Generation,
			"publishedAt": diagnostics.Identity.PublishedAt,
		},
		"environment": diagnostics.Environment, "collections": collections,
		"failedDependencyGroups": diagnostics.FailedDependencyGroups, "lastErrorCode": diagnostics.LastErrorCode,
	}
}

func (handler *Handler) listOutboxEvents(writer http.ResponseWriter, request *http.Request) {
	principal, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	pageNumber, err := positiveQueryInt(request, "page", 1)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	pageSize, err := positiveQueryInt(request, "size", 20)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	var statusFilter *outbox.Status
	if raw := strings.TrimSpace(request.URL.Query().Get("status")); raw != "" {
		value := outbox.Status(raw)
		statusFilter = &value
	}
	result, err := handler.outbox.List(request.Context(), outbox.Principal{Subject: principal.Subject, Roles: append([]string(nil), principal.Roles...)}, outbox.ListRequest{
		Status: statusFilter, PageNumber: pageNumber, PageSize: pageSize,
	})
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	events := make([]map[string]any, len(result.Events))
	for index, event := range result.Events {
		events[index] = outboxEventResponse(event)
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"events": events,
		"page":   map[string]any{"number": result.PageNumber, "size": result.PageSize, "totalNumber": result.TotalNumber, "totalPages": result.TotalPages},
	})
}

type replayOutboxEventRequest struct {
	ExpectedEventRevision outbox.LeaseRevision `json:"expectedEventRevision"`
	Reason                string               `json:"reason"`
	Confirmation          string               `json:"confirmation"`
}

func (handler *Handler) replayOutboxEvent(writer http.ResponseWriter, request *http.Request) {
	principal, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	var body replayOutboxEventRequest
	if !decodeJSON(writer, request, &body) {
		return
	}
	event, err := handler.outbox.Replay(request.Context(), outbox.ReplayCommand{
		EventID: request.PathValue("id"), ExpectedRevision: body.ExpectedEventRevision,
		Reason: body.Reason, Confirmation: body.Confirmation,
		Principal: outbox.Principal{Subject: principal.Subject, Roles: append([]string(nil), principal.Roles...)},
	})
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"event": outboxEventResponse(event)})
}

func positiveQueryInt(request *http.Request, name string, fallback int) (int, error) {
	raw := strings.TrimSpace(request.URL.Query().Get(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return value, nil
}

func optionalQueryTime(request *http.Request, name string) (*time.Time, error) {
	raw := strings.TrimSpace(request.URL.Query().Get(name))
	if raw == "" {
		return nil, nil
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be an RFC3339 timestamp", name)
	}
	value = value.UTC()
	return &value, nil
}

func outboxEventResponse(event outbox.Event) map[string]any {
	result := map[string]any{
		"id": event.ID, "sequenceNo": event.Sequence, "eventType": event.Type, "status": event.Status,
		"leaseRevision": event.LeaseRevision, "attempts": event.Attempts, "nextAttemptAt": event.NextAttemptAt,
	}
	if event.LastError != "" {
		result["lastError"] = event.LastError
	}
	return result
}

func New(queries bffapp.PageQueryPort, releases ReleaseCommands, auth Authenticator, sensitive ...bffapp.SensitiveAccessUseCase) (*Handler, error) {
	if queries == nil || isNilDependency(queries) || releases == nil || auth == nil {
		return nil, errors.New("new Admin BFF: queries, releases, and authenticator are required")
	}
	if len(sensitive) > 1 {
		return nil, errors.New("new Admin BFF: at most one sensitive access service is allowed")
	}
	if len(sensitive) == 1 && (sensitive[0] == nil || isNilDependency(sensitive[0])) {
		return nil, errors.New("new Admin BFF: sensitive access service is required when configured")
	}
	handler := &Handler{queries: queries, releases: releases, auth: auth, mux: http.NewServeMux()}
	if len(sensitive) == 1 {
		handler.sensitive = sensitive[0]
	}
	handler.mux.HandleFunc("POST /api/v1/query-page", handler.queryPage)
	handler.mux.HandleFunc("POST /api/v1/releases", handler.createRelease)
	handler.mux.HandleFunc("POST /api/v1/releases/{id}/actions", handler.actOnRelease)
	handler.mux.HandleFunc("POST /api/v1/releases/{id}/compensations", handler.createCompensatingRelease)
	if handler.sensitive != nil {
		handler.mux.HandleFunc("POST /api/v1/sensitive-fields/reveal", handler.revealSensitiveField)
	}
	return handler, nil
}

func isNilDependency(value any) bool {
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

type revealSensitiveFieldRequest struct {
	ModelCode                  string       `json:"modelCode"`
	Scope                      scopeRequest `json:"scope"`
	RecordKey                  string       `json:"recordKey"`
	FieldName                  string       `json:"fieldName"`
	ExpectedRecordRevision     uint64       `json:"expectedRecordRevision"`
	ExpectedCollectionRevision uint64       `json:"expectedCollectionRevision"`
	ExpectedModelRevision      uint64       `json:"expectedModelRevision"`
	ExpectedServerEpoch        string       `json:"expectedServerEpoch"`
	ExpectedSnapshotInstance   string       `json:"expectedSnapshotInstance"`
	ExpectedSnapshotGeneration uint64       `json:"expectedSnapshotGeneration"`
	Reason                     string       `json:"reason"`
	PreviewBucket              *int32       `json:"previewBucket,omitempty"`
}

func (handler *Handler) revealSensitiveField(writer http.ResponseWriter, request *http.Request) {
	principal, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	requestID := strings.TrimSpace(request.Header.Get("X-Request-ID"))
	if requestID == "" {
		writeError(writer, http.StatusBadRequest, "REQUEST_ID_REQUIRED", "X-Request-ID header is required")
		return
	}
	var body revealSensitiveFieldRequest
	if !decodeJSON(writer, request, &body) {
		return
	}
	traceID, traceParent, traceState := requestTraceContext(request)
	result, err := handler.sensitive.Reveal(request.Context(), bffapp.RevealSensitiveCommand{
		ModelCode: body.ModelCode, Scope: bffapp.SensitiveScope{Region: body.Scope.Region, Environment: body.Scope.Environment, Stage: body.Scope.Stage},
		RecordKey: body.RecordKey, FieldName: body.FieldName, ExpectedRecordRevision: body.ExpectedRecordRevision,
		ExpectedCollectionRevision: body.ExpectedCollectionRevision, ExpectedModelRevision: body.ExpectedModelRevision,
		ExpectedServerEpoch: body.ExpectedServerEpoch, ExpectedSnapshotInstance: body.ExpectedSnapshotInstance,
		ExpectedSnapshotGeneration: body.ExpectedSnapshotGeneration, Reason: body.Reason, PreviewBucket: body.PreviewBucket,
		RequestID: requestID, TraceID: traceID, TraceParent: traceParent, TraceState: traceState,
		Principal: bffapp.SensitivePrincipal{
			Subject: principal.Subject, DisplayName: principal.DisplayName, Roles: append([]string(nil), principal.Roles...),
			AllowedScopes: append([]platformauth.ScopePattern(nil), principal.AllowedScopes...),
		},
	})
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"value": result.Value, "expiresAt": result.ExpiresAt})
}

func requestTraceContext(request *http.Request) (string, string, string) {
	ctx := request.Context()
	if !trace.SpanContextFromContext(ctx).IsValid() {
		ctx = propagation.TraceContext{}.Extract(ctx, propagation.HeaderCarrier(request.Header))
	}
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return "", "", ""
	}
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	return spanContext.TraceID().String(), carrier.Get("traceparent"), carrier.Get("tracestate")
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	SecurityHeaders(handler.mux).ServeHTTP(writer, request)
}

// EnableOIDC mounts the browser authentication surface on the same BFF mux.
// It must be called during composition, before the handler begins serving.
func (handler *Handler) EnableOIDC(flow *OIDCFlow) error {
	if handler == nil || flow == nil {
		return errors.New("Admin BFF and OIDC flow are required")
	}
	for _, pattern := range []string{
		"GET /api/v1/auth/login",
		"GET /api/v1/auth/callback",
		"POST /api/v1/auth/logout",
		"GET /api/v1/session",
	} {
		handler.mux.Handle(pattern, flow)
	}
	return nil
}

type scopeRequest struct {
	Region      string `json:"region"`
	Environment string `json:"environment"`
	Stage       string `json:"stage,omitempty"`
}

type queryPageRequest struct {
	ModelCode     string                   `json:"modelCode"`
	Scope         scopeRequest             `json:"scope"`
	QueryType     string                   `json:"queryType"`
	PageNumber    *int32                   `json:"pageNumber,omitempty"`
	PageSize      *int32                   `json:"pageSize,omitempty"`
	Conditions    []filterConditionRequest `json:"conditions,omitempty"`
	PreviewBucket *int32                   `json:"previewBucket,omitempty"`
}

type filterConditionRequest struct {
	Field    string   `json:"field"`
	Operator string   `json:"operator"`
	Value    *string  `json:"value,omitempty"`
	Lower    *string  `json:"lower,omitempty"`
	Upper    *string  `json:"upper,omitempty"`
	Set      []string `json:"set,omitempty"`
}

func (handler *Handler) queryPage(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticate(writer, request); !ok {
		return
	}
	var body queryPageRequest
	if !decodeJSON(writer, request, &body) {
		return
	}
	queryType := bffapp.QueryType(body.QueryType)
	conditions := make([]bffapp.FilterCondition, len(body.Conditions))
	for index, condition := range body.Conditions {
		mapped := bffapp.FilterCondition{Field: condition.Field, Operator: bffapp.FilterOperator(condition.Operator), Set: make([]bffapp.ScalarValue, len(condition.Set))}
		if condition.Value != nil {
			mapped.Value = &bffapp.ScalarValue{Canonical: *condition.Value}
		}
		if condition.Lower != nil {
			mapped.Lower = &bffapp.ScalarValue{Canonical: *condition.Lower}
		}
		if condition.Upper != nil {
			mapped.Upper = &bffapp.ScalarValue{Canonical: *condition.Upper}
		}
		for valueIndex, value := range condition.Set {
			mapped.Set[valueIndex] = bffapp.ScalarValue{Canonical: value}
		}
		conditions[index] = mapped
	}
	result, err := handler.queries.QueryPage(request.Context(), bffapp.QueryRequest{
		ModelCode: body.ModelCode, Region: body.Scope.Region, Environment: body.Scope.Environment,
		Stage: body.Scope.Stage, PreviewBucket: body.PreviewBucket, Type: queryType,
		Page: bffapp.PageSpec{Number: body.PageNumber, Size: body.PageSize}, Conditions: conditions,
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
	if strings.TrimSpace(body.ReleaseTypeCode) == "" || strings.TrimSpace(body.Description) == "" || len(body.Items) == 0 {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "release type, description, and items are required")
		return
	}
	drafts := make([]application.ReleaseDraft, len(body.Items))
	for index, item := range body.Items {
		action := release.ChangeAction(item.Action)
		if action != release.ChangeAdd && action != release.ChangeModify && action != release.ChangeDelete {
			writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "release item action is invalid")
			return
		}
		drafts[index] = application.ReleaseDraft{
			Action: action, BaseBefore: item.BaseBefore, EffectiveBefore: item.EffectiveBefore, After: item.After,
			ExpectedRecordRevision: item.ExpectedRecordRevision, ExpectedCollectionRevision: item.ExpectedCollectionRevision,
			PreserveSensitiveFields: append([]string(nil), item.PreserveSensitiveFields...),
		}
	}
	result, err := handler.releases.CreateRelease(request.Context(), application.CreateReleaseCommand{
		IdempotencyKey: idempotency, ModelCode: body.ModelCode, ReleaseTypeCode: body.ReleaseTypeCode,
		Description: body.Description,
		Scope:       release.Scope{Region: body.Scope.Region, Environment: body.Scope.Environment, Stage: body.Scope.Stage},
		Actor:       principal.Subject, ActorName: principal.DisplayName, Items: drafts,
	})
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, releaseDetail(result))
}

type createCompensatingReleaseRequest struct {
	Description string `json:"description"`
}

func (handler *Handler) createCompensatingRelease(writer http.ResponseWriter, request *http.Request) {
	principal, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	idempotency := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if idempotency == "" {
		writeError(writer, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key header is required")
		return
	}
	var body createCompensatingReleaseRequest
	if !decodeJSON(writer, request, &body) {
		return
	}
	if strings.TrimSpace(body.Description) == "" {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "compensation description is required")
		return
	}
	result, err := handler.releases.CreateCompensatingRelease(request.Context(), application.CreateCompensatingReleaseCommand{
		OrderID: request.PathValue("id"), IdempotencyKey: idempotency, Description: body.Description,
		Actor: principal.Subject, ActorName: principal.DisplayName,
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
	case "APPROVE":
		action = application.ActionApprove
	case "REJECT":
		action = application.ActionReject
	case "ROLLBACK":
		action = application.ActionRollback
	default:
		writeError(writer, http.StatusNotImplemented, "NOT_IMPLEMENTED", "release action is not implemented")
		return
	}
	result, err := handler.releases.Act(request.Context(), application.ActCommand{
		OrderID: request.PathValue("id"), ActionRequestID: actionID,
		ExpectedRevision: body.ExpectedOrderRevision, ExpectedCurrentStep: body.ExpectedCurrentStep, Action: action, Actor: principal.Subject, Roles: append([]string(nil), principal.Roles...), Comment: body.Comment,
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
	case errors.Is(err, catalogapp.ErrInvalid):
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
	case errors.Is(err, catalogapp.ErrForbidden):
		writeError(writer, http.StatusForbidden, "PERMISSION_DENIED", err.Error())
	case errors.Is(err, catalogapp.ErrNotFound):
		writeError(writer, http.StatusNotFound, "NOT_FOUND", err.Error())
	case errors.Is(err, catalogapp.ErrAlreadyExists):
		writeError(writer, http.StatusConflict, "ALREADY_EXISTS", err.Error())
	case errors.Is(err, catalogapp.ErrAborted):
		writeError(writer, http.StatusConflict, "REVISION_CONFLICT", err.Error())
	case errors.Is(err, catalogapp.ErrFailedPrecondition):
		writeError(writer, http.StatusPreconditionFailed, "FAILED_PRECONDITION", err.Error())
	case errors.Is(err, audit.ErrInvalid):
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
	case errors.Is(err, audit.ErrForbidden):
		writeError(writer, http.StatusForbidden, "PERMISSION_DENIED", err.Error())
	case errors.Is(err, outbox.ErrOperationsInvalid):
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
	case errors.Is(err, outbox.ErrOperationsForbidden):
		writeError(writer, http.StatusForbidden, "PERMISSION_DENIED", err.Error())
	case errors.Is(err, outbox.ErrLeaseLost):
		writeError(writer, http.StatusConflict, "REVISION_CONFLICT", err.Error())
	case errors.Is(err, outbox.ErrNotDeadLetter):
		writeError(writer, http.StatusPreconditionFailed, "NOT_DEAD_LETTER", err.Error())
	case errors.Is(err, bffapp.ErrSensitiveInvalid):
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
	case errors.Is(err, bffapp.ErrSensitiveForbidden):
		writeError(writer, http.StatusForbidden, "PERMISSION_DENIED", err.Error())
	case errors.Is(err, bffapp.ErrSensitiveAborted):
		writeError(writer, http.StatusConflict, "ABORTED", err.Error())
	case errors.Is(err, bffapp.ErrSensitiveNotFound):
		writeError(writer, http.StatusNotFound, "NOT_FOUND", err.Error())
	case errors.Is(err, bffapp.ErrSensitiveFailedPrecondition):
		writeError(writer, http.StatusPreconditionFailed, "FAILED_PRECONDITION", err.Error())
	case errors.Is(err, bffapp.ErrPageQueryInvalid):
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
	case errors.Is(err, bffapp.ErrDiagnosticsNotFound):
		writeError(writer, http.StatusNotFound, "NOT_FOUND", "collection is not loaded")
	case errors.Is(err, bffapp.ErrDiagnosticsForbidden):
		writeError(writer, http.StatusForbidden, "PERMISSION_DENIED", "authenticated principal is not authorized")
	case errors.Is(err, release.ErrIdempotencyKeyReused):
		writeError(writer, http.StatusConflict, "IDEMPOTENCY_KEY_REUSED", err.Error())
	case errors.Is(err, release.ErrActiveConflict):
		writeError(writer, http.StatusConflict, "ACTIVE_RELEASE_CONFLICT", err.Error())
	case errors.Is(err, release.ErrForbidden):
		writeError(writer, http.StatusForbidden, "PERMISSION_DENIED", err.Error())
	case errors.Is(err, release.ErrAborted):
		writeError(writer, http.StatusConflict, "ABORTED", err.Error())
	case errors.Is(err, release.ErrFailedPrecondition):
		writeError(writer, http.StatusPreconditionFailed, "COMPARE_MISMATCH", err.Error())
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
	if view.CanExecute {
		allowed = append(allowed, "EXECUTE")
	}
	if view.CanApprove {
		allowed = append(allowed, "APPROVE")
	}
	if view.CanReject {
		allowed = append(allowed, "REJECT")
	}
	if view.CanAdvance {
		allowed = append(allowed, "ADVANCE")
	}
	if view.CanRollback {
		allowed = append(allowed, "ROLLBACK")
	}
	steps := make([]map[string]any, len(view.Steps))
	for index, step := range view.Steps {
		projected := map[string]any{"code": step.Code, "type": step.Type, "status": step.Status}
		if len(step.RolloutRanges) != 0 {
			projected["rolloutRanges"] = step.RolloutRanges
		}
		if step.CompareResult != nil {
			projected["compareResult"] = step.CompareResult
		}
		steps[index] = projected
	}
	return map[string]any{
		"order": map[string]any{
			"id": view.ID, "description": view.Description, "compensatesOrderId": view.CompensatesOrderID,
			"status": view.Status, "currentStep": view.CurrentStepCode, "currentStepType": view.CurrentStep, "currentStepStatus": view.CurrentStepStatus,
			"entityRevision": view.Revision, "canCompensate": view.CanCompensate,
		},
		"items": []any{}, "steps": steps, "allowedActions": allowed,
	}
}

func queryPageResponse(result bffapp.QueryResult) map[string]any {
	rows := make([]map[string]any, len(result.Rows))
	for index, row := range result.Rows {
		rows[index] = map[string]any{
			"recordKey": row.RecordKey, "recordRevision": row.RecordRevision, "values": row.Values,
			"maskedFields": row.MaskedFields, "basePresent": row.BasePresent,
			"baseValues": row.BaseValues, "changedFields": row.ChangedFields,
		}
	}
	fields := make([]map[string]any, len(result.InteractionFields))
	for index, field := range result.InteractionFields {
		options := make([]map[string]any, len(field.Options))
		for optionIndex, option := range field.Options {
			options[optionIndex] = map[string]any{"code": option.Code, "label": option.Label, "disabled": option.Disabled}
		}
		validationRules := make([]map[string]any, len(field.ValidationRules))
		for ruleIndex, rule := range field.ValidationRules {
			validationRules[ruleIndex] = map[string]any{"kind": rule.Kind, "params": rule.Params, "message": rule.Message}
		}
		fields[index] = map[string]any{
			"name": field.Name, "displayName": field.DisplayName, "description": field.Description,
			"type": field.Type, "uiControl": field.UIControl, "queryable": field.Queryable,
			"editable": field.Editable, "required": field.Required, "sensitive": field.Sensitive,
			"projected": field.Projected, "keyField": field.KeyField,
			"allowedFilterOperators": field.AllowedFilterOperators, "defaultFilterOperator": field.DefaultFilterOperator,
			"defaultValue": field.DefaultValue, "displayOrder": field.DisplayOrder, "validationRules": validationRules, "options": options,
		}
		if field.AutoFill != nil {
			fields[index]["autoFill"] = map[string]any{"source": field.AutoFill.Source, "value": field.AutoFill.Value}
		}
	}
	releaseTypes := make([]map[string]any, len(result.ReleaseTypes))
	for index, releaseType := range result.ReleaseTypes {
		releaseTypes[index] = map[string]any{
			"code": releaseType.Code, "name": releaseType.Name, "templateCode": releaseType.TemplateCode,
			"available": releaseType.Available,
		}
		if releaseType.UnavailableReasonCode != "" {
			releaseTypes[index]["unavailableReasonCode"] = releaseType.UnavailableReasonCode
		}
	}
	return map[string]any{
		"modelCode": result.ModelCode, "modelName": result.ModelName, "queryType": result.QueryType,
		"rows": rows, "projectionFields": result.ProjectionFields, "interactionFields": fields,
		"releaseTypes": releaseTypes,
		"page":         map[string]any{"number": result.PageNumber, "size": result.PageSize, "totalNumber": result.TotalNumber, "totalPages": result.TotalPages},
		"snapshot": map[string]any{
			"serverEpoch": result.Snapshot.ServerEpoch, "serverInstanceId": result.Snapshot.ServerInstanceID,
			"snapshotInstance": result.Snapshot.SnapshotInstance, "snapshotGeneration": result.Snapshot.Generation,
			"publishedAt": result.Snapshot.PublishedAt,
		},
		"modelRevision": result.ModelRevision, "collectionRevision": result.CollectionRevision,
	}
}
