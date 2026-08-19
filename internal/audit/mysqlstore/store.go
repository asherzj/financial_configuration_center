package mysqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/asherzj/financial_configuration_center/internal/audit"
	platformmysql "github.com/asherzj/financial_configuration_center/internal/platform/mysql"
	"gorm.io/gorm"
)

type Store struct{ database *platformmysql.Database }

func New(database *platformmysql.Database) (*Store, error) {
	if database == nil {
		return nil, errors.New("new audit MySQL store: database is required")
	}
	return &Store{database: database}, nil
}

func (store *Store) List(ctx context.Context, query audit.Query) (audit.Page, error) {
	page := audit.Page{PageNumber: query.PageNumber, PageSize: query.PageSize}
	err := store.database.WithinTransaction(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(database *gorm.DB) error {
		if err := database.WithContext(ctx).Raw(`
			SELECT COUNT(*) FROM audit_records
			WHERE (? = '' OR principal_subject = ?)
			  AND (? = '' OR resource_type = ?)
			  AND (? = '' OR resource_id = ?)
			  AND (? IS NULL OR occurred_at >= ?)
			  AND (? IS NULL OR occurred_at < ?)
		`, query.PrincipalSubject, query.PrincipalSubject, query.ResourceType, query.ResourceType, query.ResourceID, query.ResourceID, query.From, query.From, query.Until, query.Until).Scan(&page.TotalNumber).Error; err != nil {
			return err
		}
		var records []audit.Record
		offset := (query.PageNumber - 1) * query.PageSize
		if err := database.WithContext(ctx).Raw(`
			SELECT id, occurred_at, principal_subject, action, resource_type, resource_id,
				region, environment, stage, result, trace_id
			FROM audit_records
			WHERE (? = '' OR principal_subject = ?)
			  AND (? = '' OR resource_type = ?)
			  AND (? = '' OR resource_id = ?)
			  AND (? IS NULL OR occurred_at >= ?)
			  AND (? IS NULL OR occurred_at < ?)
			ORDER BY occurred_at DESC, id DESC
			LIMIT ? OFFSET ?
		`, query.PrincipalSubject, query.PrincipalSubject, query.ResourceType, query.ResourceType, query.ResourceID, query.ResourceID, query.From, query.From, query.Until, query.Until, query.PageSize, offset).Scan(&records).Error; err != nil {
			return err
		}
		page.Records = records
		return nil
	})
	if err != nil {
		return audit.Page{}, fmt.Errorf("list audit records: %w", err)
	}
	if page.TotalNumber > 0 {
		page.TotalPages = int((page.TotalNumber + int64(page.PageSize) - 1) / int64(page.PageSize))
	}
	return page, nil
}

var _ audit.Repository = (*Store)(nil)
