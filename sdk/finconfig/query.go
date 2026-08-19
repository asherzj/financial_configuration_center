package finconfig

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	catalog "github.com/asherzj/financial_configuration_center/internal/catalog/domain"
)

var (
	ErrNotStarted         = errors.New("FinConfig client has no usable snapshot")
	ErrCollectionNotFound = errors.New("FinConfig collection was not found")
	ErrInvalidQuery       = errors.New("FinConfig query is invalid")
	ErrResultTooLarge     = errors.New("FinConfig query result is too large")
)

const maxLocalQueryRows = 10_000

type Query struct {
	Collection string
	RecordKey  string
	Limit      int
}

type ReadonlyRow struct {
	recordKey string
	values    map[string]string
}

func (row ReadonlyRow) RecordKey() string { return row.recordKey }
func (row ReadonlyRow) Len() int          { return len(row.values) }
func (row ReadonlyRow) Get(field string) (string, bool) {
	value, ok := row.values[field]
	return value, ok
}
func (row ReadonlyRow) Range(visitor func(field, value string) bool) {
	if visitor == nil {
		return
	}
	fields := make([]string, 0, len(row.values))
	for field := range row.values {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		if !visitor(field, row.values[field]) {
			return
		}
	}
}
func (row ReadonlyRow) CloneMap() map[string]string { return cloneValues(row.values) }

type VersionView struct {
	Collection string
	Revision   catalog.ConfigRevision
	Digest     string
	Identity   SnapshotIdentity
}

func (client *Client) QueryOne(query Query) (ReadonlyRow, bool, error) {
	if strings.TrimSpace(query.Collection) == "" || strings.TrimSpace(query.RecordKey) == "" || query.Limit < 0 {
		return ReadonlyRow{}, false, ErrInvalidQuery
	}
	snapshot, err := client.querySnapshot()
	if err != nil {
		return ReadonlyRow{}, false, err
	}
	collection, exists := snapshot.collections[query.Collection]
	if !exists {
		return ReadonlyRow{}, false, ErrCollectionNotFound
	}
	record, exists := collection.records[query.RecordKey]
	if !exists {
		return ReadonlyRow{}, false, nil
	}
	return readonly(record), true, nil
}

func (client *Client) QueryMany(query Query) ([]ReadonlyRow, error) {
	row, found, err := client.QueryOne(query)
	if err != nil || !found {
		return []ReadonlyRow{}, err
	}
	return []ReadonlyRow{row}, nil
}

func (client *Client) QueryAll(collection string) ([]ReadonlyRow, error) {
	if strings.TrimSpace(collection) == "" {
		return nil, ErrInvalidQuery
	}
	snapshot, err := client.querySnapshot()
	if err != nil {
		return nil, err
	}
	view, exists := snapshot.collections[collection]
	if !exists {
		return nil, ErrCollectionNotFound
	}
	if len(view.records) > maxLocalQueryRows {
		return nil, ErrResultTooLarge
	}
	keys := make([]string, 0, len(view.records))
	for key := range view.records {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	rows := make([]ReadonlyRow, len(keys))
	for index, key := range keys {
		rows[index] = readonly(view.records[key])
	}
	return rows, nil
}

func (client *Client) Version(collection string) (VersionView, bool, error) {
	if strings.TrimSpace(collection) == "" {
		return VersionView{}, false, ErrInvalidQuery
	}
	snapshot, err := client.querySnapshot()
	if err != nil {
		return VersionView{}, false, err
	}
	view, exists := snapshot.collections[collection]
	if !exists {
		return VersionView{}, false, nil
	}
	return VersionView{Collection: collection, Revision: view.revision, Digest: view.digest, Identity: snapshot.identity}, true, nil
}

func (client *Client) querySnapshot() (*clientSnapshot, error) {
	if client == nil {
		return nil, ErrNotStarted
	}
	client.lifecycleMu.Lock()
	closed := client.lifecycle == lifecycleClosed
	client.lifecycleMu.Unlock()
	if closed {
		return nil, ErrClosed
	}
	snapshot := client.current.Load()
	if snapshot == nil || snapshot.identity.Generation == 0 {
		return nil, ErrNotStarted
	}
	return snapshot, nil
}

func readonly(record Record) ReadonlyRow {
	return ReadonlyRow{recordKey: record.Key, values: record.Values}
}

func Decode[T any](row ReadonlyRow) (T, error) {
	var result T
	target := reflect.ValueOf(&result).Elem()
	if target.Kind() != reflect.Struct {
		return result, errors.New("FinConfig decode target must be a struct")
	}
	typeOf := target.Type()
	for index := 0; index < target.NumField(); index++ {
		fieldType := typeOf.Field(index)
		name := fieldType.Tag.Get("finconfig")
		if name == "-" {
			continue
		}
		if name == "" {
			return result, fmt.Errorf("FinConfig decode field %s needs a finconfig tag", fieldType.Name)
		}
		value, exists := row.Get(name)
		if !exists {
			continue
		}
		if err := decodeScalar(target.Field(index), value); err != nil {
			return result, fmt.Errorf("FinConfig decode field %s: %w", name, err)
		}
	}
	return result, nil
}

func DecodeMany[T any](rows []ReadonlyRow) ([]T, error) {
	result := make([]T, len(rows))
	for index, row := range rows {
		decoded, err := Decode[T](row)
		if err != nil {
			return nil, fmt.Errorf("FinConfig decode row %d: %w", index, err)
		}
		result[index] = decoded
	}
	return result, nil
}

func decodeScalar(target reflect.Value, value string) error {
	if !target.CanSet() {
		return errors.New("target field cannot be set")
	}
	if target.Type() == reflect.TypeOf(time.Time{}) {
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return err
		}
		target.Set(reflect.ValueOf(parsed))
		return nil
	}
	switch target.Kind() {
	case reflect.String:
		target.SetString(value)
	case reflect.Bool:
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		target.SetBool(parsed)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		parsed, err := strconv.ParseInt(value, 10, target.Type().Bits())
		if err != nil {
			return err
		}
		target.SetInt(parsed)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		parsed, err := strconv.ParseUint(value, 10, target.Type().Bits())
		if err != nil {
			return err
		}
		target.SetUint(parsed)
	case reflect.Float32, reflect.Float64:
		parsed, err := strconv.ParseFloat(value, target.Type().Bits())
		if err != nil {
			return err
		}
		target.SetFloat(parsed)
	default:
		return fmt.Errorf("unsupported target type %s", target.Type())
	}
	return nil
}

func cloneValues(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}
