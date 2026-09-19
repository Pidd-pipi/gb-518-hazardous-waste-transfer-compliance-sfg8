package repository

import (
	"context"

	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/model"
	"gorm.io/gorm"
)

// QualificationSnapshotRepository owns persistence of the append-only 资质快照
// frozen alongside 转运联单提交与发运.
type QualificationSnapshotRepository interface {
	Create(ctx context.Context, snapshot *model.QualificationSnapshot) error
	NextVersion(ctx context.Context, manifestID uint) (uint, error)
	ListByManifest(ctx context.Context, manifestID uint) ([]model.QualificationSnapshot, error)
	ListByManifestCode(ctx context.Context, manifestCode string) ([]model.QualificationSnapshot, error)
	LatestForManifest(ctx context.Context, manifestCode string) (model.QualificationSnapshot, error)
}

type qualificationSnapshotRepository struct {
	db *gorm.DB
}

func NewQualificationSnapshotRepository(db *gorm.DB) QualificationSnapshotRepository {
	return &qualificationSnapshotRepository{db: db}
}

func (r *qualificationSnapshotRepository) Create(ctx context.Context, snapshot *model.QualificationSnapshot) error {
	return conn(ctx, r.db).Create(snapshot).Error
}

// NextVersion returns the next frozen snapshot version for a manifest. Callers
// must already hold the manifest row lock so concurrent transitions serialize
// and never compute the same number; the unique index is a second line of
// defence.
func (r *qualificationSnapshotRepository) NextVersion(ctx context.Context, manifestID uint) (uint, error) {
	var maxVersion uint
	if err := conn(ctx, r.db).
		Model(&model.QualificationSnapshot{}).
		Where("manifest_id = ?", manifestID).
		Select("COALESCE(MAX(version), 0)").
		Scan(&maxVersion).Error; err != nil {
		return 0, err
	}
	return maxVersion + 1, nil
}

func (r *qualificationSnapshotRepository) ListByManifest(ctx context.Context, manifestID uint) ([]model.QualificationSnapshot, error) {
	items := make([]model.QualificationSnapshot, 0)
	err := conn(ctx, r.db).
		Where("manifest_id = ?", manifestID).
		Order("version DESC, id DESC").
		Find(&items).Error
	return items, err
}

func (r *qualificationSnapshotRepository) ListByManifestCode(ctx context.Context, manifestCode string) ([]model.QualificationSnapshot, error) {
	items := make([]model.QualificationSnapshot, 0)
	err := conn(ctx, r.db).
		Where("manifest_code = ?", manifestCode).
		Order("version DESC, id DESC").
		Find(&items).Error
	return items, err
}

func (r *qualificationSnapshotRepository) LatestForManifest(ctx context.Context, manifestCode string) (model.QualificationSnapshot, error) {
	var snapshot model.QualificationSnapshot
	err := conn(ctx, r.db).
		Where("manifest_code = ?", manifestCode).
		Order("version DESC, id DESC").
		First(&snapshot).Error
	return snapshot, err
}
