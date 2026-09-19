package service_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/dto"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/model"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/repository"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/service"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type fixture struct {
	db       *gorm.DB
	tx       *repository.TxManager
	genRepo  repository.WasteGeneratorRepository
	carRepo  repository.CarrierProfileRepository
	manSvc   service.TransferManifestService
	checkSvc service.ComplianceCheckService
	snapSvc  service.QualificationSnapshotService
	manRepo  repository.TransferManifestRepository
	snapRepo repository.QualificationSnapshotRepository
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	dsn := fmt.Sprintf("file:srv-%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	// Single connection serializes SQLite writes so concurrent transitions can
	// be exercised without SQLITE_BUSY; version checks still decide the winner.
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(
		&model.User{}, &model.AuditLog{},
		&model.WasteGenerator{}, &model.CarrierProfile{},
		&model.TransferManifest{}, &model.QualificationSnapshot{}, &model.ComplianceCheck{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	genRepo := repository.NewWasteGeneratorRepository(db)
	carRepo := repository.NewCarrierProfileRepository(db)
	manRepo := repository.NewTransferManifestRepository(db)
	snapRepo := repository.NewQualificationSnapshotRepository(db)
	checkRepo := repository.NewComplianceCheckRepository(db)
	tx := repository.NewTxManager(db)
	snapSvc := service.NewQualificationSnapshotService(snapRepo, genRepo, carRepo)
	manSvc := service.NewTransferManifestService(manRepo, genRepo, carRepo, snapSvc, tx)
	checkSvc := service.NewComplianceCheckService(checkRepo, manRepo, snapSvc)
	return fixture{db: db, tx: tx, genRepo: genRepo, carRepo: carRepo, manSvc: manSvc, checkSvc: checkSvc, snapSvc: snapSvc, manRepo: manRepo, snapRepo: snapRepo}
}

func seedParties(t *testing.T, f fixture, now time.Time) (model.WasteGenerator, model.CarrierProfile) {
	t.Helper()
	generator := model.WasteGenerator{
		BaseModel:    model.BaseModel{Code: "WG-T", Name: "测试产废单位", Status: "active", Version: 1},
		PermitNumber: "PERMIT-T-1", PermitExpiresAt: now.AddDate(1, 0, 0), WasteCategories: "HW08",
		Facility: "F", Owner: "O", Category: "C", RiskLevel: "low", Evidence: "ev",
	}
	carrier := model.CarrierProfile{
		BaseModel:     model.BaseModel{Code: "CP-T", Name: "测试承运方", Status: "verified", Version: 1},
		LicenseNumber: "LIC-T-1", LicenseExpiresAt: now.AddDate(1, 0, 0), VehicleCount: 6,
		Facility: "F", Owner: "O", Category: "C", RiskLevel: "low", Evidence: "ev",
	}
	if err := f.db.Create(&generator).Error; err != nil {
		t.Fatalf("seed generator: %v", err)
	}
	if err := f.db.Create(&carrier).Error; err != nil {
		t.Fatalf("seed carrier: %v", err)
	}
	return generator, carrier
}

func createDraftManifest(t *testing.T, f fixture, code string) model.TransferManifest {
	t.Helper()
	now := time.Now().UTC()
	created, err := f.manSvc.Create(context.Background(), dto.CreateTransferManifest{
		Code: code, Name: "测试联单", GeneratorCode: "WG-T", CarrierCode: "CP-T",
		WasteCode: "HW08-900-249-08", QuantityKg: 100, Destination: "处置中心",
		Facility: "F", Owner: "O", Category: "C", RiskLevel: "low",
		EffectiveAt: now, Evidence: "minio://evidence/manifest.pdf",
	}, "tester", "req-create")
	if err != nil {
		t.Fatalf("create manifest: %v", err)
	}
	return created
}

func transitionRequest(status string, version uint) dto.TransitionRequest {
	return dto.TransitionRequest{Status: status, ExpectedVersion: version, Reason: "测试状态迁移原因"}
}

// 提交时冻结产废许可与承运资质：编号、状态、有效期、车辆数随联单快照保存。
func TestSubmitFreezesQualificationSnapshot(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC()
	gen, car := seedParties(t, f, now)
	manifest := createDraftManifest(t, f, "TM-FREEZE")

	submitted, err := f.manSvc.Transition(context.Background(), manifest.ID, transitionRequest("submitted", manifest.Version), "tester", "req-submit")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if submitted.Status != "submitted" || submitted.Version != 2 {
		t.Fatalf("manifest not submitted: status=%s version=%d", submitted.Status, submitted.Version)
	}
	snap := submitted.LatestSnapshot
	if snap == nil {
		t.Fatal("submitted manifest must hydrate its frozen snapshot")
	}
	if snap.Stage != model.SnapshotStageSubmission || snap.Version != 1 || !snap.IsValid() {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
	if snap.PermitNumber != gen.PermitNumber || snap.GeneratorStatus != "active" || snap.PermitVersion != gen.Version {
		t.Fatalf("generator permit not frozen: %+v vs %+v", snap, gen)
	}
	if !snap.PermitExpiresAt.Equal(gen.PermitExpiresAt) {
		t.Fatalf("permit expiry mismatch: %s vs %s", snap.PermitExpiresAt, gen.PermitExpiresAt)
	}
	if snap.LicenseNumber != car.LicenseNumber || snap.CarrierStatus != "verified" ||
		snap.VehicleCount != car.VehicleCount || snap.LicenseVersion != car.Version {
		t.Fatalf("carrier license not frozen: %+v vs %+v", snap, car)
	}
	if !snap.LicenseExpiresAt.Equal(car.LicenseExpiresAt) || snap.FrozenAt.IsZero() {
		t.Fatalf("license expiry/frozenAt mismatch: %+v", snap)
	}
}

// 重复或并发提交只能成功一次：一个版本号只允许一次迁移，其余冲突，刷新后仍可回读。
func TestConcurrentSubmitSucceedsOnce(t *testing.T) {
	f := newFixture(t)
	seedParties(t, f, time.Now().UTC())
	manifest := createDraftManifest(t, f, "TM-CONCURRENT")

	const n = 5
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			<-start
			_, results[idx] = f.manSvc.Transition(context.Background(), manifest.ID, transitionRequest("submitted", manifest.Version), "tester", fmt.Sprintf("req-%d", idx))
		}(i)
	}
	close(start)
	wg.Wait()

	success, conflict := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, repository.ErrVersionConflict):
			conflict++
		default:
			t.Fatalf("unexpected concurrent error: %v", err)
		}
	}
	if success != 1 || conflict != n-1 {
		t.Fatalf("expected exactly one success, got success=%d conflict=%d", success, conflict)
	}
	refreshed, err := f.manSvc.Get(context.Background(), manifest.ID)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if refreshed.Status != "submitted" || refreshed.Version != 2 {
		t.Fatalf("readback wrong state: status=%s version=%d", refreshed.Status, refreshed.Version)
	}
	history, err := f.snapRepo.ListByManifest(context.Background(), manifest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 {
		t.Fatalf("exactly one snapshot may be frozen by concurrent submit, got %d", len(history))
	}
}

// 发运前实时停用：整单拒绝、联单状态与版本不变，但失效快照（含原因）被冻结。
func TestDispatchRejectsLiveSuspensionWithoutStateChange(t *testing.T) {
	f := newFixture(t)
	seedParties(t, f, time.Now().UTC())
	manifest := createDraftManifest(t, f, "TM-DISPATCH-BLOCK")

	submitted, err := f.manSvc.Transition(context.Background(), manifest.ID, transitionRequest("submitted", 1), "tester", "req-submit")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	if err := f.db.Model(&model.CarrierProfile{}).Where("code = ?", "CP-T").
		Updates(map[string]any{"status": "suspended", "vehicle_count": 0}).Error; err != nil {
		t.Fatal(err)
	}

	refused, err := f.manSvc.Transition(context.Background(), manifest.ID, transitionRequest("in_transit", submitted.Version), "tester", "req-dispatch-blocked")
	if !errors.Is(err, service.ErrQualificationInvalid) {
		t.Fatalf("expected ErrQualificationInvalid, got %v", err)
	}
	if refused.Status != "submitted" || refused.Version != submitted.Version {
		t.Fatalf("status must remain unchanged after refusal: status=%s version=%d", refused.Status, refused.Version)
	}
	snap := refused.LatestSnapshot
	if snap == nil || snap.Stage != model.SnapshotStageDispatch || snap.IsValid() {
		t.Fatalf("expected invalid dispatch snapshot, got %+v", snap)
	}
	if snap.CarrierStatus != "suspended" || snap.VehicleCount != 0 || snap.InvalidReason == "" {
		t.Fatalf("invalid snapshot must freeze live status, vehicles and reason: %+v", snap)
	}
	persisted, err := f.manRepo.Get(context.Background(), manifest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != "submitted" || persisted.Version != submitted.Version {
		t.Fatalf("persisted row changed despite refused dispatch: status=%s version=%d", persisted.Status, persisted.Version)
	}
}

// 发运前许可过期同样实时拒绝，且历史快照不被覆盖；恢复后发运成功，追加新版本快照。
func TestExpiredLicenseRejectedThenHistoryStaysImmutable(t *testing.T) {
	f := newFixture(t)
	seedParties(t, f, time.Now().UTC())
	manifest := createDraftManifest(t, f, "TM-EXPIRE")

	submitted, err := f.manSvc.Transition(context.Background(), manifest.ID, transitionRequest("submitted", 1), "tester", "req-submit")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	v1 := submitted.LatestSnapshot

	past := time.Now().UTC().Add(-time.Hour)
	if err := f.db.Model(&model.CarrierProfile{}).Where("code = ?", "CP-T").
		Updates(map[string]any{"license_expires_at": past}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.manSvc.Transition(context.Background(), manifest.ID, transitionRequest("in_transit", submitted.Version), "tester", "req-dispatch-expired"); !errors.Is(err, service.ErrQualificationInvalid) {
		t.Fatalf("expected ErrQualificationInvalid for expired license, got %v", err)
	}

	// Restore and modify license identity: even after a change the old frozen
	// snapshots must keep their original numbers/expiry/vehicle counts.
	future := time.Now().UTC().AddDate(2, 0, 0)
	if err := f.db.Model(&model.CarrierProfile{}).Where("code = ?", "CP-T").
		Updates(map[string]any{"status": "verified", "license_expires_at": future, "license_number": "LIC-CHANGED", "vehicle_count": 99, "version": 7}).Error; err != nil {
		t.Fatal(err)
	}
	inTransit, err := f.manSvc.Transition(context.Background(), manifest.ID, transitionRequest("in_transit", submitted.Version), "tester", "req-dispatch-ok")
	if err != nil {
		t.Fatalf("dispatch after restore: %v", err)
	}
	if inTransit.Status != "in_transit" {
		t.Fatalf("expected in_transit, got %s", inTransit.Status)
	}
	history, err := f.snapSvc.ListByManifestCode(context.Background(), manifest.Code)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 {
		t.Fatalf("expected submission + invalid dispatch + valid dispatch = 3 snapshots, got %d", len(history))
	}
	// history is newest first: v3 valid dispatch, v2 invalid expiry, v1 submission.
	v3, v2, frozenV1 := history[0], history[1], history[2]
	if v3.Version != 3 || !v3.IsValid() || v3.LicenseNumber != "LIC-CHANGED" || v3.VehicleCount != 99 {
		t.Fatalf("v3 should capture changed license: %+v", v3)
	}
	if v2.Version != 2 || v2.IsValid() || v2.InvalidReason == "" || v2.LicenseNumber != "LIC-T-1" {
		t.Fatalf("v2 should retain invalid expiry snapshot with old license: %+v", v2)
	}
	if frozenV1.Version != 1 || !frozenV1.IsValid() || frozenV1.LicenseNumber != v1.LicenseNumber ||
		frozenV1.VehicleCount != v1.VehicleCount || !frozenV1.LicenseExpiresAt.Equal(v1.LicenseExpiresAt) {
		t.Fatalf("v1 history was rewritten by later changes: %+v vs %+v", frozenV1, v1)
	}
}

// 核验页只按冻结快照决定：无快照不可通过；失效快照不可通过；快照有效而证照事后停用
// 仍可通过，且决定依据写入快照版本与有效期。
func TestComplianceDecisionUsesFrozenSnapshotOnly(t *testing.T) {
	f := newFixture(t)
	seedParties(t, f, time.Now().UTC())

	// Manifest that was never submitted: no snapshot, pass must be refused.
	draft := createDraftManifest(t, f, "TM-NO-SNAPSHOT")
	draftCheck := createCheck(t, f, "CC-NO-SNAPSHOT", draft.Code)
	if _, err := f.checkSvc.Transition(context.Background(), draftCheck.ID, transitionRequest("pass", draftCheck.Version), "reviewer", "req-pass-nosnap"); !errors.Is(err, service.ErrSnapshotNotFrozen) {
		t.Fatalf("expected ErrSnapshotNotFrozen, got %v", err)
	}

	// Submitted then invalid dispatch: latest snapshot invalid, pass refused but
	// fail is allowed and records its decision basis.
	blocked := createDraftManifest(t, f, "TM-BAD-SNAPSHOT")
	submitted, err := f.manSvc.Transition(context.Background(), blocked.ID, transitionRequest("submitted", 1), "tester", "req-submit2")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.CarrierProfile{}).Where("code = ?", "CP-T").
		Updates(map[string]any{"status": "suspended"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.manSvc.Transition(context.Background(), blocked.ID, transitionRequest("in_transit", submitted.Version), "tester", "req-dispatch2"); !errors.Is(err, service.ErrQualificationInvalid) {
		t.Fatalf("expected refusal, got %v", err)
	}
	badCheck := createCheck(t, f, "CC-BAD", blocked.Code)
	if _, err := f.checkSvc.Transition(context.Background(), badCheck.ID, transitionRequest("pass", badCheck.Version), "reviewer", "req-pass-bad"); !errors.Is(err, service.ErrSnapshotInvalid) {
		t.Fatalf("expected ErrSnapshotInvalid, got %v", err)
	}
	failed, err := f.checkSvc.Transition(context.Background(), badCheck.ID, transitionRequest("fail", badCheck.Version), "reviewer", "req-fail-bad")
	if err != nil {
		t.Fatalf("fail decision should be allowed: %v", err)
	}
	if failed.Status != "fail" || failed.FrozenSnapshot == nil || failed.FrozenSnapshot.IsValid() {
		t.Fatalf("fail decision must hydrate the invalid frozen snapshot: %+v", failed.FrozenSnapshot)
	}

	// Valid submitted manifest: restore the live carrier, submit, then suspend
	// it again after the snapshot is frozen. The pass decision must still
	// succeed from the snapshot alone.
	if err := f.db.Model(&model.CarrierProfile{}).Where("code = ?", "CP-T").
		Updates(map[string]any{"status": "verified"}).Error; err != nil {
		t.Fatal(err)
	}
	good := createDraftManifest(t, f, "TM-GOOD-SNAPSHOT")
	goodSubmitted, err := f.manSvc.Transition(context.Background(), good.ID, transitionRequest("submitted", 1), "tester", "req-submit3")
	if err != nil {
		t.Fatal(err)
	}
	goodCheck := createCheck(t, f, "CC-GOOD", good.Code)
	// Live data now disagrees with the frozen valid snapshot, but the review
	// page decides from the snapshot.
	if err := f.db.Model(&model.CarrierProfile{}).Where("code = ?", "CP-T").
		Updates(map[string]any{"status": "suspended"}).Error; err != nil {
		t.Fatal(err)
	}
	passed, err := f.checkSvc.Transition(context.Background(), goodCheck.ID, transitionRequest("pass", goodCheck.Version), "reviewer", "req-pass-good")
	if err != nil {
		t.Fatalf("pass must rely on frozen snapshot despite live suspension: %v", err)
	}
	if passed.Status != "pass" || passed.FrozenSnapshot == nil || passed.FrozenSnapshot.Version != goodSubmitted.LatestSnapshot.Version {
		t.Fatalf("pass decision not anchored to frozen snapshot: %+v", passed)
	}
	if !strings.Contains(passed.DecisionBasis, "v1") || !strings.Contains(passed.DecisionBasis, "PERMIT-T-1") || !strings.Contains(passed.DecisionBasis, "LIC-T-1") {
		t.Fatalf("decision basis must cite snapshot version and license numbers: %s", passed.DecisionBasis)
	}
}

// 已驳回联单即使快照有效也不能通过核验（流程资格仍由联单状态决定）。
func TestRejectedManifestCannotPass(t *testing.T) {
	f := newFixture(t)
	seedParties(t, f, time.Now().UTC())
	manifest := createDraftManifest(t, f, "TM-REJECTED")
	submitted, err := f.manSvc.Transition(context.Background(), manifest.ID, transitionRequest("submitted", 1), "tester", "req-submit4")
	if err != nil {
		t.Fatal(err)
	}
	rejected, err := f.manSvc.Transition(context.Background(), manifest.ID, transitionRequest("rejected", submitted.Version), "tester", "req-reject")
	if err != nil {
		t.Fatal(err)
	}
	check := createCheck(t, f, "CC-REJECTED", rejected.Code)
	if _, err := f.checkSvc.Transition(context.Background(), check.ID, transitionRequest("pass", check.Version), "reviewer", "req-pass-rejected"); err == nil {
		t.Fatal("rejected manifest must not pass compliance review")
	}
}

func createCheck(t *testing.T, f fixture, code, manifestCode string) model.ComplianceCheck {
	t.Helper()
	check, err := f.checkSvc.Create(context.Background(), dto.CreateComplianceCheck{
		Code: code, Name: "测试核验", ManifestCode: manifestCode,
		Checklist: "产废许可、承运资质、联单数量、处置去向",
		Facility:  "F", Owner: "O", Category: "C", RiskLevel: "low",
		EffectiveAt: time.Now().UTC(), Evidence: "minio://evidence/check.pdf",
	}, "operator", "req-check-create")
	if err != nil {
		t.Fatalf("create check: %v", err)
	}
	return check
}
