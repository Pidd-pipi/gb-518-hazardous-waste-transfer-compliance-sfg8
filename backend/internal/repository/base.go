package repository

import (
	"context"
	"errors"
	"strings"

	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/dto"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrVersionConflict = errors.New("record was changed by another request")

type Page[T any] struct {
	Items    []T   `json:"items"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"pageSize"`
}

// Store centralizes consistent paging and optimistic-lock semantics while
// concrete repository files retain an explicit boundary for each aggregate.
type Store[T any] struct {
	db            *gorm.DB
	supportsLocks bool
}

func NewStore[T any](db *gorm.DB) *Store[T] {
	return &Store[T]{db: db, supportsLocks: db.Dialector.Name() != "sqlite"}
}

// GetForUpdate reads a row with a pessimistic write lock (SELECT ... FOR UPDATE)
// so concurrent state-machine transitions serialize on the same aggregate.
// SQLite serializes writes on its own and has no row-lock syntax, so there the
// clause is omitted. Must be called inside a transaction.
func (s *Store[T]) GetForUpdate(ctx context.Context, id uint) (T, error) {
	var item T
	query := conn(ctx, s.db)
	if s.supportsLocks {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := query.First(&item, id).Error
	return item, err
}

// FindByCodeForUpdate is the code-keyed variant of GetForUpdate.
func (s *Store[T]) FindByCodeForUpdate(ctx context.Context, code string) (T, error) {
	var item T
	query := conn(ctx, s.db)
	if s.supportsLocks {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := query.Where("UPPER(code) = ?", strings.ToUpper(strings.TrimSpace(code))).First(&item).Error
	return item, err
}

func (s *Store[T]) List(ctx context.Context, query dto.PageQuery) (Page[T], error) {
	page, pageSize := normalizePage(query.Page, query.PageSize)
	db := conn(ctx, s.db).Model(new(T))
	if search := strings.TrimSpace(strings.ToLower(query.Search)); search != "" {
		wildcard := "%" + search + "%"
		db = db.Where("LOWER(code) LIKE ? OR LOWER(name) LIKE ?", wildcard, wildcard)
	}
	if status := strings.TrimSpace(query.Status); status != "" {
		db = db.Where("status = ?", status)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return Page[T]{}, err
	}
	items := make([]T, 0)
	err := db.Order("updated_at DESC, id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error
	return Page[T]{Items: items, Total: total, Page: page, PageSize: pageSize}, err
}

func (s *Store[T]) Get(ctx context.Context, id uint) (T, error) {
	var item T
	err := conn(ctx, s.db).First(&item, id).Error
	return item, err
}

func (s *Store[T]) FindByCode(ctx context.Context, code string) (T, error) {
	var item T
	err := conn(ctx, s.db).Where("UPPER(code) = ?", strings.ToUpper(strings.TrimSpace(code))).First(&item).Error
	return item, err
}

func (s *Store[T]) Create(ctx context.Context, item *T) error {
	return conn(ctx, s.db).Create(item).Error
}

func (s *Store[T]) CreateAudited(ctx context.Context, item *T, audit *model.AuditLog) error {
	return inUnitOfWork(ctx, s.db, func(q *gorm.DB) error {
		if err := q.Create(item).Error; err != nil {
			return err
		}
		record, ok := any(item).(model.DomainRecord)
		if !ok {
			return errors.New("audited model does not expose its base record")
		}
		audit.EntityID = record.GetBase().ID
		return q.Create(audit).Error
	})
}

func (s *Store[T]) Update(ctx context.Context, id, expectedVersion uint, item *T) error {
	return updateRecord(conn(ctx, s.db), id, expectedVersion, item)
}

func (s *Store[T]) UpdateAudited(ctx context.Context, id, expectedVersion uint, item *T, audit *model.AuditLog) error {
	return inUnitOfWork(ctx, s.db, func(q *gorm.DB) error {
		if err := updateRecord(q, id, expectedVersion, item); err != nil {
			return err
		}
		audit.EntityID = id
		return q.Create(audit).Error
	})
}

func updateRecord[T any](db *gorm.DB, id, expectedVersion uint, item *T) error {
	result := db.Model(new(T)).
		Where("id = ? AND version = ?", id, expectedVersion).
		Select("*").Omit("id", "code", "created_at", "deleted_at").Updates(item)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrVersionConflict
	}
	return nil
}

func (s *Store[T]) Delete(ctx context.Context, id uint) error {
	return deleteRecord[T](conn(ctx, s.db), id)
}

func (s *Store[T]) DeleteAudited(ctx context.Context, id uint, audit *model.AuditLog) error {
	return inUnitOfWork(ctx, s.db, func(q *gorm.DB) error {
		if err := deleteRecord[T](q, id); err != nil {
			return err
		}
		audit.EntityID = id
		return q.Create(audit).Error
	})
}

func deleteRecord[T any](db *gorm.DB, id uint) error {
	result := db.Delete(new(T), id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *Store[T]) CountByStatus(ctx context.Context) (map[string]int64, error) {
	rows, err := conn(ctx, s.db).Model(new(T)).
		Select("status, COUNT(*) AS total").Group("status").Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := make(map[string]int64)
	for rows.Next() {
		var status string
		var total int64
		if err := rows.Scan(&status, &total); err != nil {
			return nil, err
		}
		counts[status] = total
	}
	return counts, rows.Err()
}

func normalizePage(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}
