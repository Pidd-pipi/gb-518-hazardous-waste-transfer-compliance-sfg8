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

type ComplianceCheckService interface {
	List(context.Context, dto.PageQuery) (repository.Page[model.ComplianceCheck], error)
	Get(context.Context, uint) (model.ComplianceCheck, error)
	Create(context.Context, dto.CreateComplianceCheck, string, string) (model.ComplianceCheck, error)
	Update(context.Context, uint, dto.UpdateComplianceCheck, string, string) (model.ComplianceCheck, error)
	Transition(context.Context, uint, dto.TransitionRequest, string, string) (model.ComplianceCheck, error)
	Delete(context.Context, uint, string, string) error
	StatusCounts(context.Context) (map[string]int64, error)
}

type complianceCheckService struct {
	repository repository.ComplianceCheckRepository
	manifests  repository.TransferManifestRepository
	snapshots  QualificationSnapshotService
}

func NewComplianceCheckService(repo repository.ComplianceCheckRepository, manifests repository.TransferManifestRepository, snapshots QualificationSnapshotService) ComplianceCheckService {
	return &complianceCheckService{repository: repo, manifests: manifests, snapshots: snapshots}
}

func (s *complianceCheckService) List(ctx context.Context, query dto.PageQuery) (repository.Page[model.ComplianceCheck], error) {
	page, err := s.repository.List(ctx, query)
	if err != nil {
		return page, err
	}
	s.snapshots.HydrateChecks(ctx, page.Items)
	return page, nil
}

func (s *complianceCheckService) Get(ctx context.Context, id uint) (model.ComplianceCheck, error) {
	item, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.ComplianceCheck{}, err
	}
	if err := s.snapshots.HydrateCheck(ctx, &item); err != nil {
		return model.ComplianceCheck{}, err
	}
	return item, nil
}

func (s *complianceCheckService) Create(ctx context.Context, input dto.CreateComplianceCheck, actor, requestID string) (model.ComplianceCheck, error) {
	if err := validateComplianceCheckBusinessFields(input.Code, input.Name, input.Facility, input.Owner, input.ManifestCode, input.Checklist, input.Evidence); err != nil {
		return model.ComplianceCheck{}, err
	}
	if _, err := s.manifests.FindByCode(ctx, input.ManifestCode); err != nil {
		return model.ComplianceCheck{}, fmt.Errorf("%w: manifest %s does not exist", ErrInvalidInput, input.ManifestCode)
	}
	item := model.ComplianceCheck{
		BaseModel: model.BaseModel{
			Code: strings.ToUpper(strings.TrimSpace(input.Code)), Name: strings.TrimSpace(input.Name),
			Status: model.ComplianceCheckInitialStatus, Version: 1, Description: strings.TrimSpace(input.Description),
		},
		ManifestCode: strings.ToUpper(strings.TrimSpace(input.ManifestCode)), Checklist: strings.TrimSpace(input.Checklist), DecisionBasis: strings.TrimSpace(input.DecisionBasis),
		Facility: strings.TrimSpace(input.Facility), Owner: strings.TrimSpace(input.Owner),
		Category: strings.TrimSpace(input.Category), RiskLevel: input.RiskLevel,
		MetricValue: input.MetricValue, MetricUnit: strings.TrimSpace(input.MetricUnit),
		EffectiveAt: input.EffectiveAt.UTC(), Evidence: strings.TrimSpace(input.Evidence),
		RelatedCode: strings.ToUpper(strings.TrimSpace(input.RelatedCode)),
	}
	if err := s.repository.CreateAudited(ctx, &item, newAuditLog(actor, requestID, "create", "ComplianceCheck", "", item.Status, "created compliance evidence set")); err != nil {
		return model.ComplianceCheck{}, fmt.Errorf("create 合规核验: %w", err)
	}
	return item, nil
}

func (s *complianceCheckService) Update(ctx context.Context, id uint, input dto.UpdateComplianceCheck, actor, requestID string) (model.ComplianceCheck, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.ComplianceCheck{}, err
	}
	if current.Status != "pending" {
		return model.ComplianceCheck{}, fmt.Errorf("%w: decided checks are immutable", ErrInvalidInput)
	}
	if err := validateComplianceCheckBusinessFields(current.Code, input.Name, input.Facility, input.Owner, input.ManifestCode, input.Checklist, input.Evidence); err != nil {
		return model.ComplianceCheck{}, err
	}
	if _, err := s.manifests.FindByCode(ctx, input.ManifestCode); err != nil {
		return model.ComplianceCheck{}, fmt.Errorf("%w: manifest %s does not exist", ErrInvalidInput, input.ManifestCode)
	}
	current.Name = strings.TrimSpace(input.Name)
	current.ManifestCode = strings.ToUpper(strings.TrimSpace(input.ManifestCode))
	current.Checklist = strings.TrimSpace(input.Checklist)
	current.DecisionBasis = strings.TrimSpace(input.DecisionBasis)
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
	if err := s.repository.UpdateAudited(ctx, id, input.ExpectedVersion, &current, newAuditLog(actor, requestID, "update", "ComplianceCheck", current.Status, current.Status, "updated pending evidence set")); err != nil {
		return model.ComplianceCheck{}, fmt.Errorf("update 合规核验: %w", err)
	}
	return s.repository.Get(ctx, id)
}

func (s *complianceCheckService) Transition(ctx context.Context, id uint, input dto.TransitionRequest, actor, requestID string) (model.ComplianceCheck, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.ComplianceCheck{}, err
	}
	target := strings.TrimSpace(input.Status)
	if !constants.CanTransition(constants.ComplianceCheckTransitions, current.Status, target) {
		return model.ComplianceCheck{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current.Status, target)
	}
	// 核验页只按冻结快照作出决定：不读取证照实时数据，证照之后变化也不影响本页。
	history, err := s.snapshots.ListByManifestCode(ctx, current.ManifestCode)
	if err != nil {
		return model.ComplianceCheck{}, fmt.Errorf("%w: linked manifest snapshot is unavailable", ErrInvalidInput)
	}
	var latest *model.QualificationSnapshot
	if len(history) > 0 {
		latest = &history[0]
	}
	if latest == nil {
		return model.ComplianceCheck{}, fmt.Errorf("%w: manifest %s has no frozen qualification snapshot; submit or dispatch it before review", ErrSnapshotNotFrozen, current.ManifestCode)
	}
	if target == string(constants.CheckStatePass) {
		// 流程资格仍来自联单自身状态：已驳回联单不能通过核验。资质有效性则只看
		// 冻结快照，不读取证照实时数据，证照之后变化不影响本决定。
		manifest, manifestErr := s.manifests.FindByCode(ctx, current.ManifestCode)
		if manifestErr != nil {
			return model.ComplianceCheck{}, fmt.Errorf("%w: linked manifest is unavailable", ErrInvalidInput)
		}
		if manifest.Status == string(constants.ManifestStateRejected) {
			return model.ComplianceCheck{}, fmt.Errorf("%w: a rejected manifest cannot pass compliance review", ErrInvalidInput)
		}
		if !latest.IsValid() {
			return model.ComplianceCheck{}, fmt.Errorf("%w: 快照 v%d 已记录发运前失效（%s），不能通过", ErrSnapshotInvalid, latest.Version, latest.InvalidReason)
		}
		if latest.Stage != model.SnapshotStageSubmission && latest.Stage != model.SnapshotStageDispatch {
			return model.ComplianceCheck{}, fmt.Errorf("%w: snapshot stage %s cannot support a pass decision", ErrSnapshotInvalid, latest.Stage)
		}
	}
	if strings.TrimSpace(current.Evidence) == "" || strings.TrimSpace(input.Reason) == "" {
		return model.ComplianceCheck{}, fmt.Errorf("%w: decision evidence and reason are required", ErrInvalidInput)
	}
	before := current.Status
	current.Status = target
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	current.DecisionBasis = fmt.Sprintf("%s（依据冻结资质快照 v%d/%s：%s 证 %s、%s 证 %s，车辆 %d 辆，有效期 %s / %s）",
		strings.TrimSpace(input.Reason),
		latest.Version, stageLabel(latest.Stage),
		latest.GeneratorCode, latest.PermitNumber,
		latest.CarrierCode, latest.LicenseNumber,
		latest.VehicleCount,
		latest.PermitExpiresAt.Format(time.RFC3339),
		latest.LicenseExpiresAt.Format(time.RFC3339),
	)
	if err := s.repository.UpdateAudited(ctx, id, input.ExpectedVersion, &current, newAuditLog(actor, requestID, "transition", "ComplianceCheck", before, target, current.DecisionBasis)); err != nil {
		return model.ComplianceCheck{}, fmt.Errorf("transition 合规核验: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *complianceCheckService) Delete(ctx context.Context, id uint, actor, requestID string) error {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return err
	}
	if current.Status != "pending" {
		return fmt.Errorf("%w: decided checks must be retained for compliance", ErrInvalidInput)
	}
	return s.repository.DeleteAudited(ctx, id, newAuditLog(actor, requestID, "delete", "ComplianceCheck", current.Status, "deleted", "soft deleted pending check"))
}

func (s *complianceCheckService) StatusCounts(ctx context.Context) (map[string]int64, error) {
	return s.repository.CountByStatus(ctx)
}

func validateComplianceCheckBusinessFields(code, name, facility, owner, manifestCode, checklist, evidence string) error {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(name) == "" || strings.TrimSpace(facility) == "" || strings.TrimSpace(owner) == "" || strings.TrimSpace(manifestCode) == "" || strings.TrimSpace(checklist) == "" {
		return fmt.Errorf("%w: check identity, manifest and checklist are required", ErrInvalidInput)
	}
	if strings.TrimSpace(evidence) == "" {
		return fmt.Errorf("%w: compliance evidence reference is required", ErrInvalidInput)
	}
	return nil
}
