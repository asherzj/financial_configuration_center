package mysqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/asherzj/financial_configuration_center/internal/outbox"
	platformmysql "github.com/asherzj/financial_configuration_center/internal/platform/mysql"
	"gorm.io/gorm"
)

type Store struct{ database *platformmysql.Database }

func New(database *platformmysql.Database) (*Store, error) {
	if database == nil {
		return nil, errors.New("new outbox MySQL store: database is required")
	}
	return &Store{database: database}, nil
}

func (store *Store) Claim(ctx context.Context, request outbox.ClaimRequest) ([]outbox.Event, error) {
	if request.WorkerID == "" || request.Limit <= 0 || request.LeaseDuration <= 0 || request.Now.IsZero() {
		return nil, errors.New("claim outbox request is invalid")
	}
	var claimed []outbox.Event
	err := store.database.WithinTransaction(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(database *gorm.DB) error {
		type row struct {
			ID, AggregateType, AggregateID, EventType, IdempotencyKey, Status string
			SequenceNo, LeaseRevision                                         uint64
			PayloadVersion                                                    uint32
			Payload                                                           []byte
			Attempts                                                          int
			LockedBy                                                          *string
			LockedUntil                                                       *time.Time
		}
		var rows []row
		result := database.WithContext(ctx).Raw(`
			SELECT id, sequence_no, aggregate_type, aggregate_id, event_type,
				payload_version, payload, idempotency_key, status, lease_revision,
				attempts, locked_by, locked_until
			FROM outbox_events
			WHERE (status = 'PENDING' AND next_attempt_at <= ?)
				OR (status = 'PROCESSING' AND locked_until <= ?)
			ORDER BY sequence_no
			LIMIT ? FOR UPDATE SKIP LOCKED
		`, request.Now.UTC(), request.Now.UTC(), request.Limit).Scan(&rows)
		if result.Error != nil {
			return result.Error
		}
		claimed = make([]outbox.Event, 0, len(rows))
		lockedUntil := request.Now.UTC().Add(request.LeaseDuration)
		for _, row := range rows {
			updated := database.WithContext(ctx).Exec(`
				UPDATE outbox_events
				SET status = 'PROCESSING', lease_revision = lease_revision + 1,
					attempts = attempts + 1, locked_by = ?, locked_until = ?, updated_at = ?
				WHERE id = ? AND lease_revision = ?
			`, request.WorkerID, lockedUntil, request.Now.UTC(), row.ID, row.LeaseRevision)
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected != 1 {
				return fmt.Errorf("%w: event %s changed while claimed", outbox.ErrLeaseLost, row.ID)
			}
			claimed = append(claimed, outbox.Event{
				ID: row.ID, Sequence: row.SequenceNo, AggregateType: row.AggregateType, AggregateID: row.AggregateID,
				Type: row.EventType, PayloadVersion: row.PayloadVersion, Payload: append([]byte(nil), row.Payload...), IdempotencyKey: row.IdempotencyKey,
				Status: outbox.StatusProcessing, LeaseRevision: outbox.LeaseRevision(row.LeaseRevision + 1), Attempts: row.Attempts + 1,
				LockedBy: request.WorkerID, LockedUntil: lockedUntil,
			})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("claim outbox events: %w", err)
	}
	return claimed, nil
}

func (store *Store) MarkSent(ctx context.Context, event outbox.Event, publishedAt time.Time) error {
	return store.updateLease(ctx, func(database *gorm.DB) error {
		result := database.WithContext(ctx).Exec(`
			UPDATE outbox_events
			SET status = 'SENT', lease_revision = lease_revision + 1,
				locked_by = NULL, locked_until = NULL, last_error = NULL,
				published_at = ?, updated_at = ?
			WHERE id = ? AND status = 'PROCESSING' AND lease_revision = ? AND locked_by = ?
		`, publishedAt.UTC(), publishedAt.UTC(), event.ID, event.LeaseRevision, event.LockedBy)
		return requireLeaseUpdate(result, event.ID)
	})
}

func (store *Store) MarkFailed(ctx context.Context, event outbox.Event, summary string, nextAttemptAt time.Time, maxAttempts int, failedAt time.Time) (outbox.Status, error) {
	if maxAttempts <= 0 {
		return "", errors.New("outbox max attempts must be positive")
	}
	status := outbox.StatusPending
	if event.Attempts >= maxAttempts {
		status = outbox.StatusDeadLetter
	}
	err := store.updateLease(ctx, func(database *gorm.DB) error {
		result := database.WithContext(ctx).Exec(`
			UPDATE outbox_events
			SET status = ?, lease_revision = lease_revision + 1,
				next_attempt_at = ?, locked_by = NULL, locked_until = NULL,
				last_error = ?, updated_at = ?
			WHERE id = ? AND status = 'PROCESSING' AND lease_revision = ? AND locked_by = ?
		`, status, nextAttemptAt.UTC(), truncate(summary, 1024), failedAt.UTC(), event.ID, event.LeaseRevision, event.LockedBy)
		return requireLeaseUpdate(result, event.ID)
	})
	return status, err
}

func (store *Store) Replay(ctx context.Context, request outbox.ReplayRequest) (outbox.Event, error) {
	if request.EventID == "" || request.ExpectedRevision == 0 || request.Reason == "" || request.Actor == "" || request.Now.IsZero() {
		return outbox.Event{}, errors.New("outbox replay request is incomplete")
	}
	var replayed outbox.Event
	err := store.database.WithinTransaction(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(database *gorm.DB) error {
		type row struct {
			ID, AggregateType, AggregateID, EventType, IdempotencyKey, Status string
			SequenceNo, LeaseRevision                                         uint64
			PayloadVersion                                                    uint32
			Payload                                                           []byte
			Attempts                                                          int
		}
		var loaded row
		result := database.WithContext(ctx).Raw(`
			SELECT id, sequence_no, aggregate_type, aggregate_id, event_type,
				payload_version, payload, idempotency_key, status, lease_revision, attempts
			FROM outbox_events WHERE id = ? FOR UPDATE
		`, request.EventID).Scan(&loaded)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 || outbox.LeaseRevision(loaded.LeaseRevision) != request.ExpectedRevision {
			return fmt.Errorf("%w: event %s", outbox.ErrLeaseLost, request.EventID)
		}
		if outbox.Status(loaded.Status) != outbox.StatusDeadLetter {
			return fmt.Errorf("%w: event %s", outbox.ErrNotDeadLetter, request.EventID)
		}
		updated := database.WithContext(ctx).Exec(`
			UPDATE outbox_events
			SET status = 'PENDING', lease_revision = lease_revision + 1, attempts = 0,
				next_attempt_at = ?, locked_by = NULL, locked_until = NULL,
				last_error = NULL, published_at = NULL, updated_at = ?
			WHERE id = ? AND status = 'DEAD_LETTER' AND lease_revision = ?
		`, request.Now.UTC(), request.Now.UTC(), request.EventID, request.ExpectedRevision)
		if err := requireLeaseUpdate(updated, request.EventID); err != nil {
			return err
		}
		if err := database.WithContext(ctx).Exec(`
			INSERT INTO audit_records (
				occurred_at, principal_subject, principal_display_name, action,
				resource_type, resource_id, region, environment, stage, result,
				before_data, after_data, metadata, request_id, trace_id
			) VALUES (?, ?, ?, 'OUTBOX_REPLAY', 'OUTBOX_EVENT', ?, '', '', '', 'SUCCEEDED',
				NULL, NULL, JSON_OBJECT('reason', ?, 'previousLeaseRevision', ?), ?, '')
		`, request.Now.UTC(), request.Actor, request.Actor, request.EventID, truncate(request.Reason, 1024), request.ExpectedRevision, request.EventID).Error; err != nil {
			return err
		}
		replayed = outbox.Event{
			ID: loaded.ID, Sequence: loaded.SequenceNo, AggregateType: loaded.AggregateType, AggregateID: loaded.AggregateID,
			Type: loaded.EventType, PayloadVersion: loaded.PayloadVersion, Payload: append([]byte(nil), loaded.Payload...), IdempotencyKey: loaded.IdempotencyKey,
			Status: outbox.StatusPending, LeaseRevision: outbox.LeaseRevision(loaded.LeaseRevision + 1), Attempts: 0, NextAttemptAt: request.Now.UTC(),
		}
		return nil
	})
	if err != nil {
		return outbox.Event{}, fmt.Errorf("replay outbox event: %w", err)
	}
	return replayed, nil
}

func (store *Store) List(ctx context.Context, request outbox.ListRequest) (outbox.EventPage, error) {
	status := ""
	if request.Status != nil {
		status = string(*request.Status)
	}
	page := outbox.EventPage{PageNumber: request.PageNumber, PageSize: request.PageSize}
	err := store.database.WithinTransaction(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(database *gorm.DB) error {
		if err := database.WithContext(ctx).Raw(`
			SELECT COUNT(*) FROM outbox_events WHERE ? = '' OR status = ?
		`, status, status).Scan(&page.TotalNumber).Error; err != nil {
			return err
		}
		type row struct {
			ID, AggregateType, AggregateID, EventType, IdempotencyKey, Status string
			SequenceNo, LeaseRevision                                         uint64
			PayloadVersion                                                    uint32
			Attempts                                                          int
			NextAttemptAt                                                     time.Time
			LastError                                                         *string
		}
		var rows []row
		offset := (request.PageNumber - 1) * request.PageSize
		if err := database.WithContext(ctx).Raw(`
			SELECT id, sequence_no, aggregate_type, aggregate_id, event_type,
				payload_version, idempotency_key, status, lease_revision, attempts,
				next_attempt_at, last_error
			FROM outbox_events
			WHERE ? = '' OR status = ?
			ORDER BY sequence_no DESC
			LIMIT ? OFFSET ?
		`, status, status, request.PageSize, offset).Scan(&rows).Error; err != nil {
			return err
		}
		page.Events = make([]outbox.Event, len(rows))
		for index, row := range rows {
			lastError := ""
			if row.LastError != nil {
				lastError = *row.LastError
			}
			page.Events[index] = outbox.Event{
				ID: row.ID, Sequence: row.SequenceNo, AggregateType: row.AggregateType, AggregateID: row.AggregateID,
				Type: row.EventType, PayloadVersion: row.PayloadVersion, IdempotencyKey: row.IdempotencyKey,
				Status: outbox.Status(row.Status), LeaseRevision: outbox.LeaseRevision(row.LeaseRevision), Attempts: row.Attempts,
				NextAttemptAt: row.NextAttemptAt, LastError: lastError,
			}
		}
		return nil
	})
	if err != nil {
		return outbox.EventPage{}, fmt.Errorf("list outbox events: %w", err)
	}
	if page.TotalNumber > 0 {
		page.TotalPages = int((page.TotalNumber + int64(page.PageSize) - 1) / int64(page.PageSize))
	}
	return page, nil
}

func (store *Store) updateLease(ctx context.Context, update func(*gorm.DB) error) error {
	err := store.database.WithinTransaction(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, update)
	if err != nil {
		return fmt.Errorf("update outbox lease: %w", err)
	}
	return nil
}

func requireLeaseUpdate(result *gorm.DB, eventID string) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("%w: event %s", outbox.ErrLeaseLost, eventID)
	}
	return nil
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

var _ outbox.Repository = (*Store)(nil)
var _ outbox.OperationsRepository = (*Store)(nil)
