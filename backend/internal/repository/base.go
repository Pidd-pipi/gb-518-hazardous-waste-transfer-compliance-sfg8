package repository

import (
	"context"
	"errors"
	"strings"

	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/dto"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/model"
	"gorm.io/gorm"
)

var ErrVersionConflict = errors.New("record was changed by another request")

// IsDuplicateKey reports whether the error is a unique-constraint violation
// from any supported database driver, so repeated submissions of the same
// business code can be answered with a conflict instead of a server error.
func IsDuplicateKey(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint failed") || // sqlite
		strings.Contains(message, "duplicate key") || // postgres
		strings.Contains(message, "duplicate entry") // mysql
}

type Page[T any] struct {
	Items    []T   `json:"items"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"pageSize"`
}

// Store centralizes consistent paging and optimistic-lock semantics while
// concrete repository files retain an explicit boundary for each aggregate.
type Store[T any] struct {
	db *gorm.DB
}

func NewStore[T any](db *gorm.DB) *Store[T] { return &Store[T]{db: db} }

func (s *Store[T]) List(ctx context.Context, query dto.PageQuery) (Page[T], error) {
	page, pageSize := normalizePage(query.Page, query.PageSize)
	db := s.db.WithContext(ctx).Model(new(T))
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
	err := s.db.WithContext(ctx).First(&item, id).Error
	return item, err
}

func (s *Store[T]) FindByCode(ctx context.Context, code string) (T, error) {
	var item T
	err := s.db.WithContext(ctx).Where("UPPER(code) = ?", strings.ToUpper(strings.TrimSpace(code))).First(&item).Error
	return item, err
}

// ListByCodes loads every record whose code is in the given set. Codes are
// normalized and de-duplicated so callers can pass raw user input.
func (s *Store[T]) ListByCodes(ctx context.Context, codes []string) ([]T, error) {
	normalized := make([]string, 0, len(codes))
	seen := make(map[string]bool, len(codes))
	for _, code := range codes {
		code = strings.ToUpper(strings.TrimSpace(code))
		if code == "" || seen[code] {
			continue
		}
		seen[code] = true
		normalized = append(normalized, code)
	}
	items := make([]T, 0, len(normalized))
	if len(normalized) == 0 {
		return items, nil
	}
	err := s.db.WithContext(ctx).Where("UPPER(code) IN ?", normalized).Find(&items).Error
	return items, err
}

func (s *Store[T]) Create(ctx context.Context, item *T) error {
	return s.db.WithContext(ctx).Create(item).Error
}

func (s *Store[T]) CreateAudited(ctx context.Context, item *T, audit *model.AuditLog) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(item).Error; err != nil {
			return err
		}
		record, ok := any(item).(model.DomainRecord)
		if !ok {
			return errors.New("audited model does not expose its base record")
		}
		audit.EntityID = record.GetBase().ID
		return tx.Create(audit).Error
	})
}

func (s *Store[T]) Update(ctx context.Context, id, expectedVersion uint, item *T) error {
	return updateRecord(s.db.WithContext(ctx), id, expectedVersion, item)
}

func (s *Store[T]) UpdateAudited(ctx context.Context, id, expectedVersion uint, item *T, audit *model.AuditLog) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := updateRecord(tx, id, expectedVersion, item); err != nil {
			return err
		}
		audit.EntityID = id
		return tx.Create(audit).Error
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
	return deleteRecord[T](s.db.WithContext(ctx), id)
}

func (s *Store[T]) DeleteAudited(ctx context.Context, id uint, audit *model.AuditLog) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := deleteRecord[T](tx, id); err != nil {
			return err
		}
		audit.EntityID = id
		return tx.Create(audit).Error
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
	rows, err := s.db.WithContext(ctx).Model(new(T)).
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
