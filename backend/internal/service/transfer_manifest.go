package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/constants"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/dto"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/model"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/repository"
)

type TransferManifestService interface {
	List(context.Context, dto.PageQuery) (repository.Page[model.TransferManifest], error)
	Get(context.Context, uint) (model.TransferManifest, error)
	Create(context.Context, dto.CreateTransferManifest, string, string) (model.TransferManifest, error)
	Update(context.Context, uint, dto.UpdateTransferManifest, string, string) (model.TransferManifest, error)
	Transition(context.Context, uint, dto.TransitionRequest, string, string) (model.TransferManifest, error)
	Delete(context.Context, uint, string, string) error
	StatusCounts(context.Context) (map[string]int64, error)
	SnapshotHistory(context.Context, string) ([]model.QualificationSnapshot, error)
}

type transferManifestService struct {
	repository repository.TransferManifestRepository
	generators repository.WasteGeneratorRepository
	carriers   repository.CarrierProfileRepository
	snapshots  QualificationSnapshotService
	tx         *repository.TxManager
}

func NewTransferManifestService(repo repository.TransferManifestRepository, generators repository.WasteGeneratorRepository, carriers repository.CarrierProfileRepository, snapshots QualificationSnapshotService, tx *repository.TxManager) TransferManifestService {
	return &transferManifestService{repository: repo, generators: generators, carriers: carriers, snapshots: snapshots, tx: tx}
}

func (s *transferManifestService) List(ctx context.Context, query dto.PageQuery) (repository.Page[model.TransferManifest], error) {
	page, err := s.repository.List(ctx, query)
	if err != nil {
		return page, err
	}
	s.snapshots.HydrateManifests(ctx, page.Items)
	return page, nil
}

func (s *transferManifestService) Get(ctx context.Context, id uint) (model.TransferManifest, error) {
	item, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.TransferManifest{}, err
	}
	if err := s.snapshots.HydrateManifest(ctx, &item); err != nil {
		return model.TransferManifest{}, err
	}
	return item, nil
}

func (s *transferManifestService) Create(ctx context.Context, input dto.CreateTransferManifest, actor, requestID string) (model.TransferManifest, error) {
	if err := validateTransferManifestBusinessFields(input.Code, input.Name, input.Facility, input.Owner, input.GeneratorCode, input.CarrierCode, input.WasteCode, input.Destination, input.Evidence, input.QuantityKg); err != nil {
		return model.TransferManifest{}, err
	}
	if _, err := s.generators.FindByCode(ctx, input.GeneratorCode); err != nil {
		return model.TransferManifest{}, fmt.Errorf("%w: generator %s does not exist", ErrInvalidInput, input.GeneratorCode)
	}
	if _, err := s.carriers.FindByCode(ctx, input.CarrierCode); err != nil {
		return model.TransferManifest{}, fmt.Errorf("%w: carrier %s does not exist", ErrInvalidInput, input.CarrierCode)
	}
	item := model.TransferManifest{
		BaseModel: model.BaseModel{
			Code: strings.ToUpper(strings.TrimSpace(input.Code)), Name: strings.TrimSpace(input.Name),
			Status: model.TransferManifestInitialStatus, Version: 1, Description: strings.TrimSpace(input.Description),
		},
		GeneratorCode: strings.ToUpper(strings.TrimSpace(input.GeneratorCode)), CarrierCode: strings.ToUpper(strings.TrimSpace(input.CarrierCode)),
		WasteCode: strings.ToUpper(strings.TrimSpace(input.WasteCode)), QuantityKg: input.QuantityKg, Destination: strings.TrimSpace(input.Destination),
		Facility: strings.TrimSpace(input.Facility), Owner: strings.TrimSpace(input.Owner),
		Category: strings.TrimSpace(input.Category), RiskLevel: input.RiskLevel,
		MetricValue: input.MetricValue, MetricUnit: strings.TrimSpace(input.MetricUnit),
		EffectiveAt: input.EffectiveAt.UTC(), Evidence: strings.TrimSpace(input.Evidence),
		RelatedCode: strings.ToUpper(strings.TrimSpace(input.RelatedCode)),
	}
	if err := s.repository.CreateAudited(ctx, &item, newAuditLog(actor, requestID, "create", "TransferManifest", "", item.Status, "created linked transfer manifest")); err != nil {
		return model.TransferManifest{}, fmt.Errorf("create 转运清单: %w", err)
	}
	return item, nil
}

func (s *transferManifestService) Update(ctx context.Context, id uint, input dto.UpdateTransferManifest, actor, requestID string) (model.TransferManifest, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.TransferManifest{}, err
	}
	if current.Status != "draft" {
		return model.TransferManifest{}, fmt.Errorf("%w: only draft manifests can be edited", ErrInvalidInput)
	}
	if err := validateTransferManifestBusinessFields(current.Code, input.Name, input.Facility, input.Owner, input.GeneratorCode, input.CarrierCode, input.WasteCode, input.Destination, input.Evidence, input.QuantityKg); err != nil {
		return model.TransferManifest{}, err
	}
	if _, err := s.generators.FindByCode(ctx, input.GeneratorCode); err != nil {
		return model.TransferManifest{}, fmt.Errorf("%w: generator %s does not exist", ErrInvalidInput, input.GeneratorCode)
	}
	if _, err := s.carriers.FindByCode(ctx, input.CarrierCode); err != nil {
		return model.TransferManifest{}, fmt.Errorf("%w: carrier %s does not exist", ErrInvalidInput, input.CarrierCode)
	}
	current.Name = strings.TrimSpace(input.Name)
	current.GeneratorCode = strings.ToUpper(strings.TrimSpace(input.GeneratorCode))
	current.CarrierCode = strings.ToUpper(strings.TrimSpace(input.CarrierCode))
	current.WasteCode = strings.ToUpper(strings.TrimSpace(input.WasteCode))
	current.QuantityKg = input.QuantityKg
	current.Destination = strings.TrimSpace(input.Destination)
	current.Description = strings.TrimSpace(input.Description)
	current.Facility = strings.TrimSpace(input.Facility)
	current.Owner = strings.TrimSpace(input.Owner)
	current.Category = strings.TrimSpace(input.Category)
	current.RiskLevel = input.RiskLevel
	current.MetricValue = input.MetricValue
	current.MetricUnit = strings.TrimSpace(input.MetricUnit)
	current.EffectiveAt = input.EffectiveAt.UTC()
	current.Evidence = strings.TrimSpace(input.Evidence)
	current.RelatedCode = strings.ToUpper(strings.TrimSpace(input.RelatedCode))
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	if err := s.repository.UpdateAudited(ctx, id, input.ExpectedVersion, &current, newAuditLog(actor, requestID, "update", "TransferManifest", current.Status, current.Status, "updated draft manifest and evidence")); err != nil {
		return model.TransferManifest{}, fmt.Errorf("update 转运清单: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *transferManifestService) Transition(ctx context.Context, id uint, input dto.TransitionRequest, actor, requestID string) (model.TransferManifest, error) {
	target := strings.TrimSpace(input.Status)

	var snapshot *model.QualificationSnapshot
	var qualificationErr error
	err := s.tx.InTx(ctx, func(txCtx context.Context) error {
		// Read the manifest with a row lock inside the transaction: duplicate and
		// concurrent requests serialize here. The optimistic-version check runs
		// before the state-machine check, so a losing request always reports a
		// conflict (409) regardless of the new state it happens to observe.
		locked, lockErr := s.repository.GetForUpdate(txCtx, id)
		if lockErr != nil {
			return lockErr
		}
		current := locked
		if current.Version != input.ExpectedVersion {
			return repository.ErrVersionConflict
		}
		if !constants.CanTransition(constants.TransferManifestTransitions, current.Status, target) {
			return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current.Status, target)
		}

		if target == "submitted" || target == "in_transit" {
			stage := model.SnapshotStageSubmission
			if target == "in_transit" {
				stage = model.SnapshotStageDispatch
			}
			frozen, freezeErr := s.snapshots.FreezeForTransition(txCtx, current, stage)
			if freezeErr != nil {
				return freezeErr
			}
			snapshot = frozen
			if !frozen.IsValid() {
				// 发运前实时失效/停用：冻结 valid=false 快照后返回业务错误。联单状态与
				// version 保持不变（事务正常提交，只落库快照与审计），整单拒绝。
				qualificationErr = fmt.Errorf("%w: %s", ErrQualificationInvalid, frozen.InvalidReason)
				if auditErr := s.appendAudit(txCtx, actor, requestID, current.Status, target, stage, frozen); auditErr != nil {
					return auditErr
				}
				return nil
			}
		}

		before := current.Status
		current.Status = target
		current.Version = input.ExpectedVersion + 1
		current.UpdatedAt = time.Now().UTC()
		detail := input.Reason
		if snapshot != nil {
			detail = fmt.Sprintf("%s（资质快照 v%d：%s/%s 有效）", input.Reason, snapshot.Version, snapshot.PermitNumber, snapshot.LicenseNumber)
		}
		if updateErr := s.repository.UpdateAudited(txCtx, id, input.ExpectedVersion, &current,
			newAuditLog(actor, requestID, "transition", "TransferManifest", before, target, detail)); updateErr != nil {
			return fmt.Errorf("transition 转运清单: %w", updateErr)
		}
		if snapshot != nil {
			if auditErr := s.appendAudit(txCtx, actor, requestID, before, target, snapshot.Stage, snapshot); auditErr != nil {
				return auditErr
			}
		}
		return nil
	})
	if err != nil {
		return model.TransferManifest{}, err
	}
	if qualificationErr != nil {
		// Status deliberately unchanged: re-read so the client can 刷新后回读 the
		// same draft/submitted row together with the newly frozen failure snapshot.
		refreshed, refreshErr := s.Get(ctx, id)
		if refreshErr != nil {
			return model.TransferManifest{}, qualificationErr
		}
		return refreshed, qualificationErr
	}
	return s.Get(ctx, id)
}

// appendAudit records that a qualification snapshot was frozen. For valid moves
// this complements the state-transition audit entry; for rejected dispatches it
// is the only audit entry because the manifest status did not change.
func (s *transferManifestService) appendAudit(ctx context.Context, actor, requestID, before, after, stage string, snapshot *model.QualificationSnapshot) error {
	action := "qualification_frozen"
	detail := fmt.Sprintf("冻结%s资质快照 v%d（valid=%t）", stageLabel(stage), snapshot.Version, snapshot.IsValid())
	if !snapshot.IsValid() {
		detail = fmt.Sprintf("冻结%s资质快照 v%d（整单拒绝：%s）", stageLabel(stage), snapshot.Version, snapshot.InvalidReason)
		after = before
	}
	audit := newAuditLog(actor, requestID, action, "QualificationSnapshot", before, after, detail)
	audit.EntityID = snapshot.ManifestID
	return s.repository.AppendAudit(ctx, audit)
}

func stageLabel(stage string) string {
	if stage == model.SnapshotStageDispatch {
		return "发运"
	}
	return "提交"
}

func (s *transferManifestService) Delete(ctx context.Context, id uint, actor, requestID string) error {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return err
	}
	if current.Status != "draft" {
		return fmt.Errorf("%w: submitted manifests must be retained for compliance", ErrInvalidInput)
	}
	return s.repository.DeleteAudited(ctx, id, newAuditLog(actor, requestID, "delete", "TransferManifest", current.Status, "deleted", "soft deleted draft manifest"))
}

func (s *transferManifestService) StatusCounts(ctx context.Context) (map[string]int64, error) {
	return s.repository.CountByStatus(ctx)
}

func (s *transferManifestService) SnapshotHistory(ctx context.Context, manifestCode string) ([]model.QualificationSnapshot, error) {
	history, err := s.snapshots.ListByManifestCode(ctx, manifestCode)
	if err != nil {
		return nil, err
	}
	return history, nil
}

func validateTransferManifestBusinessFields(code, name, facility, owner, generatorCode, carrierCode, wasteCode, destination, evidence string, quantityKg float64) error {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(name) == "" || strings.TrimSpace(facility) == "" || strings.TrimSpace(owner) == "" || strings.TrimSpace(generatorCode) == "" || strings.TrimSpace(carrierCode) == "" || strings.TrimSpace(wasteCode) == "" || strings.TrimSpace(destination) == "" {
		return fmt.Errorf("%w: manifest identity, parties and route are required", ErrInvalidInput)
	}
	if quantityKg <= 0 || strings.TrimSpace(evidence) == "" {
		return fmt.Errorf("%w: positive waste quantity and manifest evidence are required", ErrInvalidInput)
	}
	return nil
}
