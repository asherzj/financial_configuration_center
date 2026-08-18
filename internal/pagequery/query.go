package pagequery

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
	"github.com/asherzj/financial_configuration_center/internal/distribution/snapshot"
)

type SnapshotProvider interface {
	Current() *snapshot.Snapshot
}

type QueryType string

const (
	TypeAll      QueryType = "ALL"
	TypeOnlyData QueryType = "ONLY_DATA"
)

type PageSpec struct {
	Number *int32
	Size   *int32
}

type Request struct {
	ModelCode   string
	Environment string
	Type        QueryType
	Page        PageSpec
}

type InteractionField struct {
	Name                   string
	DisplayName            string
	Description            string
	Type                   catalog.FieldType
	UIControl              catalog.UIControlType
	Queryable              bool
	Editable               bool
	Required               bool
	Sensitive              bool
	Projected              bool
	KeyField               bool
	AllowedFilterOperators []catalog.FilterOperator
	DefaultFilterOperator  catalog.FilterOperator
	DefaultValue           *string
	DisplayOrder           int32
}

type Row struct {
	RecordKey      string
	RecordRevision catalog.ConfigRevision
	Values         map[string]string
	MaskedFields   []string
}

type Result struct {
	ModelCode          string
	ModelName          string
	QueryType          QueryType
	Rows               []Row
	ProjectionFields   []string
	InteractionFields  []InteractionField
	PageNumber         int32
	PageSize           int32
	TotalNumber        int64
	TotalPages         int64
	Snapshot           snapshot.Identity
	ModelRevision      catalog.ConfigRevision
	CollectionRevision catalog.ConfigRevision
}

type Querier struct{ snapshots SnapshotProvider }

func New(snapshots SnapshotProvider) *Querier { return &Querier{snapshots: snapshots} }

func (querier *Querier) Query(request Request) (Result, error) {
	if querier == nil || querier.snapshots == nil {
		return Result{}, errors.New("page query: snapshot provider is required")
	}
	request.ModelCode = strings.TrimSpace(request.ModelCode)
	request.Environment = strings.TrimSpace(request.Environment)
	if request.ModelCode == "" || request.Environment == "" {
		return Result{}, errors.New("page query: model and environment are required")
	}
	if request.Type != TypeAll && request.Type != TypeOnlyData {
		return Result{}, fmt.Errorf("page query: unsupported query type %q", request.Type)
	}
	current := querier.snapshots.Current()
	if current == nil || current.Environment() != request.Environment {
		return Result{}, errors.New("page query: requested environment is not loaded")
	}
	model, exists := current.Model(request.ModelCode)
	if !exists {
		return Result{}, fmt.Errorf("page query: model %q is not loaded", request.ModelCode)
	}
	definition, exists := current.Definition(model.Collection())
	if !exists {
		return Result{}, errors.New("page query: model collection is not loaded")
	}
	collectionRevision, exists := current.CollectionVersion(model.Collection())
	if !exists {
		return Result{}, errors.New("page query: collection version is not loaded")
	}

	pageNumber, pageSize, err := normalizePage(request.Page, model.DefaultPageSize(), model.MaxPageSize())
	if err != nil {
		return Result{}, err
	}
	projection := model.ProjectionFields()
	projectionSet := make(map[string]struct{}, len(projection))
	for _, field := range projection {
		projectionSet[field] = struct{}{}
	}
	records := current.Records(model.Collection())
	rows := make([]Row, len(records))
	modelFields := model.Fields()
	for index, record := range records {
		values := make(map[string]string, len(projection))
		for _, field := range projection {
			if value, present := record.Data[field]; present {
				values[field] = value
			}
		}
		var masked []string
		for _, field := range modelFields {
			if field.Sensitive {
				if _, present := record.Data[field.Name]; present {
					masked = append(masked, field.Name)
				}
			}
		}
		sort.Strings(masked)
		rows[index] = Row{RecordKey: record.RecordKey, RecordRevision: record.ConfigRevision, Values: values, MaskedFields: masked}
	}
	total := int64(len(rows))
	totalPages := int64(0)
	if total > 0 {
		totalPages = (total + int64(pageSize) - 1) / int64(pageSize)
	}
	start := int64(pageNumber-1) * int64(pageSize)
	end := start + int64(pageSize)
	if start >= total {
		rows = []Row{}
	} else {
		if end > total {
			end = total
		}
		rows = rows[start:end]
	}

	result := Result{
		ModelCode: model.Code(), ModelName: model.Name(), QueryType: request.Type,
		Rows: rows, PageNumber: pageNumber, PageSize: pageSize, TotalNumber: total, TotalPages: totalPages,
		Snapshot: current.Identity(), ModelRevision: model.ConfigRevision(), CollectionRevision: collectionRevision,
	}
	if request.Type == TypeAll {
		result.ProjectionFields = slices.Clone(projection)
		result.InteractionFields = interactionFields(definition, model, projectionSet)
	}
	return result, nil
}

func normalizePage(page PageSpec, defaultSize, maxSize int32) (int32, int32, error) {
	number := int32(1)
	if page.Number != nil {
		if *page.Number <= 0 {
			return 0, 0, errors.New("page query: explicit page number must be positive")
		}
		number = *page.Number
	}
	size := defaultSize
	if page.Size != nil {
		if *page.Size <= 0 {
			return 0, 0, errors.New("page query: explicit page size must be positive")
		}
		size = *page.Size
	}
	if size > maxSize {
		return 0, 0, fmt.Errorf("page query: page size %d exceeds maximum %d", size, maxSize)
	}
	return number, size, nil
}

func interactionFields(definition catalog.CollectionDefinition, model catalog.CompiledModel, projection map[string]struct{}) []InteractionField {
	keySet := make(map[string]struct{}, len(model.KeyFields()))
	for _, field := range model.KeyFields() {
		keySet[field] = struct{}{}
	}
	modelFields := model.Fields()
	fields := make([]InteractionField, len(modelFields))
	for index, modelField := range modelFields {
		definitionField, _ := definition.Field(modelField.Name)
		_, projected := projection[modelField.Name]
		_, key := keySet[modelField.Name]
		var defaultOperator catalog.FilterOperator
		if len(modelField.AllowedFilterOperators) > 0 {
			defaultOperator = modelField.AllowedFilterOperators[0]
		}
		fields[index] = InteractionField{
			Name: modelField.Name, DisplayName: definitionField.DisplayName, Description: definitionField.Description,
			Type: modelField.Type, UIControl: modelField.UIControl, Queryable: modelField.Queryable,
			Editable: modelField.Editable, Required: modelField.Required, Sensitive: modelField.Sensitive,
			Projected: projected, KeyField: key, AllowedFilterOperators: slices.Clone(modelField.AllowedFilterOperators),
			DefaultFilterOperator: defaultOperator, DefaultValue: cloneStringPointer(modelField.DefaultValue), DisplayOrder: definitionField.DisplayOrder,
		}
	}
	sort.SliceStable(fields, func(left, right int) bool { return fields[left].DisplayOrder < fields[right].DisplayOrder })
	return fields
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
