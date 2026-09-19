package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/model"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/repository"
	"gorm.io/gorm"
)

// QualificationSnapshotService freezes 产废许可 and 承运资质 evidence at 联单提交
// and 发运 time. It is the only place that reads live license rows for a
// transition: the captured values are copied into an append-only snapshot, and
// every later decision (合规核验) reads the snapshot instead of live data.
type QualificationSnapshotService interface {
	// FreezeForTransition re-reads both licenses under row locks, validates them
	// for the given stage and appends an immutable snapshot. For a dispatch that
	// fails because a license went invalid/suspended in real time the snapshot
	// is still frozen with valid=false and the failure reason, while the caller
	// leaves the manifest status untouched.
	FreezeForTransition(ctx context.Context, manifest model.TransferManifest, stage string) (*model.QualificationSnapshot, error)
	HydrateManifest(ctx context.Context, manifest *model.TransferManifest) error
	HydrateManifests(ctx context.Context, manifests []model.TransferManifest)
	HydrateCheck(ctx context.Context, check *model.ComplianceCheck) error
	HydrateChecks(ctx context.Context, checks []model.ComplianceCheck)
	ListByManifestCode(ctx context.Context, manifestCode string) ([]model.QualificationSnapshot, error)
}

type qualificationSnapshotService struct {
	snapshots  repository.QualificationSnapshotRepository
	generators repository.WasteGeneratorRepository
	carriers   repository.CarrierProfileRepository
}

func NewQualificationSnapshotService(snapshots repository.QualificationSnapshotRepository, generators repository.WasteGeneratorRepository, carriers repository.CarrierProfileRepository) QualificationSnapshotService {
	return &qualificationSnapshotService{snapshots: snapshots, generators: generators, carriers: carriers}
}

func (s *qualificationSnapshotService) FreezeForTransition(ctx context.Context, manifest model.TransferManifest, stage string) (*model.QualificationSnapshot, error) {
	// Lock both license rows so their status/permit fields cannot change while
	// the manifest transition and the snapshot are being committed.
	generator, err := s.generators.FindByCodeForUpdate(ctx, manifest.GeneratorCode)
	if err != nil {
		return nil, fmt.Errorf("%w: generator %s is unavailable", ErrInvalidInput, manifest.GeneratorCode)
	}
	carrier, err := s.carriers.FindByCodeForUpdate(ctx, manifest.CarrierCode)
	if err != nil {
		return nil, fmt.Errorf("%w: carrier %s is unavailable", ErrInvalidInput, manifest.CarrierCode)
	}

	now := time.Now().UTC()
	valid, reason := evaluateQualifications(generator, carrier, now, stage)
	nextVersion, err := s.snapshots.NextVersion(ctx, manifest.ID)
	if err != nil {
		return nil, fmt.Errorf("allocate snapshot version: %w", err)
	}
	validValue := valid
	snapshot := model.QualificationSnapshot{
		ManifestID:       manifest.ID,
		ManifestCode:     manifest.Code,
		Stage:            stage,
		Version:          nextVersion,
		Valid:            &validValue,
		InvalidReason:    reason,
		GeneratorCode:    generator.Code,
		GeneratorStatus:  generator.Status,
		PermitNumber:     generator.PermitNumber,
		PermitVersion:    generator.Version,
		PermitExpiresAt:  generator.PermitExpiresAt.UTC(),
		CarrierCode:      carrier.Code,
		CarrierStatus:    carrier.Status,
		LicenseNumber:    carrier.LicenseNumber,
		LicenseVersion:   carrier.Version,
		LicenseExpiresAt: carrier.LicenseExpiresAt.UTC(),
		VehicleCount:     carrier.VehicleCount,
		FrozenAt:         now,
	}
	if err := s.snapshots.Create(ctx, &snapshot); err != nil {
		return nil, fmt.Errorf("freeze qualification snapshot: %w", err)
	}
	return &snapshot, nil
}

func (s *qualificationSnapshotService) HydrateManifest(ctx context.Context, manifest *model.TransferManifest) error {
	snapshot, err := s.snapshots.LatestForManifest(ctx, manifest.Code)
	if err == nil {
		manifest.LatestSnapshot = &snapshot
		return nil
	}
	if err == gorm.ErrRecordNotFound {
		return nil
	}
	return err
}

func (s *qualificationSnapshotService) HydrateManifests(ctx context.Context, manifests []model.TransferManifest) {
	for i := range manifests {
		_ = s.HydrateManifest(ctx, &manifests[i])
	}
}

func (s *qualificationSnapshotService) HydrateCheck(ctx context.Context, check *model.ComplianceCheck) error {
	snapshot, err := s.snapshots.LatestForManifest(ctx, check.ManifestCode)
	if err == nil {
		check.FrozenSnapshot = &snapshot
		return nil
	}
	if err == gorm.ErrRecordNotFound {
		return nil
	}
	return err
}

func (s *qualificationSnapshotService) HydrateChecks(ctx context.Context, checks []model.ComplianceCheck) {
	for i := range checks {
		_ = s.HydrateCheck(ctx, &checks[i])
	}
}

func (s *qualificationSnapshotService) ListByManifestCode(ctx context.Context, manifestCode string) ([]model.QualificationSnapshot, error) {
	return s.snapshots.ListByManifestCode(ctx, strings.ToUpper(strings.TrimSpace(manifestCode)))
}

// evaluateQualifications re-validates live licenses at the moment of 提交 or
// 发运. 提交要求 active/verified 且未过期；发运在此之外还要求至少一辆有效车辆。
// The returned human-readable reason is frozen onto invalid snapshots so the
// 核验页 can show why a dispatch was refused without re-reading live data.
func evaluateQualifications(generator model.WasteGenerator, carrier model.CarrierProfile, now time.Time, stage string) (bool, string) {
	if generator.Status != "active" {
		return false, fmt.Sprintf("产废许可当前状态为 %s，发运前已停用，整单拒绝", generator.Status)
	}
	if !generator.PermitExpiresAt.UTC().After(now) {
		return false, "产废许可证已过有效期，发运前实时失效，整单拒绝"
	}
	if carrier.Status != "verified" {
		return false, fmt.Sprintf("承运资质当前状态为 %s，发运前已停用，整单拒绝", carrier.Status)
	}
	if !carrier.LicenseExpiresAt.UTC().After(now) {
		return false, "承运许可证已过有效期，发运前实时失效，整单拒绝"
	}
	if stage == model.SnapshotStageDispatch && carrier.VehicleCount <= 0 {
		return false, "承运方有效车辆数为 0，不具备发运条件，整单拒绝"
	}
	return true, ""
}
