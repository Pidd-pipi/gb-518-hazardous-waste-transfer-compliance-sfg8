package router_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/config"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/database"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/router"
	"github.com/gin-gonic/gin"
)

type apiEnvelope struct {
	Data  json.RawMessage `json:"data"`
	Error string          `json:"error"`
	Meta  struct {
		Total int64 `json:"total"`
	} `json:"meta"`
}

type record struct {
	ID      uint   `json:"id"`
	Code    string `json:"code"`
	Status  string `json:"status"`
	Version uint   `json:"version"`
}

type manifestSnapshotRecord struct {
	ID                    uint   `json:"id"`
	Code                  string `json:"code"`
	Status                string `json:"status"`
	Version               uint   `json:"version"`
	SnapshotVersion       uint   `json:"snapshotVersion"`
	GeneratorPermitNumber string `json:"generatorPermitNumber"`
	GeneratorPermitStatus string `json:"generatorPermitStatus"`
	CarrierLicenseNumber  string `json:"carrierLicenseNumber"`
	CarrierLicenseStatus  string `json:"carrierLicenseStatus"`
	CarrierVehicleCount   int    `json:"carrierVehicleCount"`
	SnapshotInvalidReason string `json:"snapshotInvalidReason"`
}

func decodeManifestSnapshot(t *testing.T, body []byte) manifestSnapshotRecord {
	t.Helper()
	var envelope struct {
		Data manifestSnapshotRecord `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Data.ID == 0 {
		t.Fatalf("decode manifest snapshot: %v body=%s", err, string(body))
	}
	return envelope.Data
}

func TestQualificationSnapshotClosedLoop(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := testConfig(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, _, err := database.Open(context.Background(), cfg, logger)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	engine := router.New(cfg, db, nil, logger)

	operator := login(t, engine, "operator")
	reviewer := login(t, engine, "reviewer")

	// 重复创建同一编码只能成功一次。
	response, body := request(t, engine, http.MethodPost, "/api/manifests", operator, "snap-create", manifestPayload("TM-SNAP-001", "CP-002"))
	assertStatus(t, response, http.StatusCreated)
	manifest := decodeManifestSnapshot(t, body)
	if manifest.SnapshotVersion != 0 || manifest.SnapshotInvalidReason != "" {
		t.Fatalf("draft manifest must not carry a snapshot: %+v", manifest)
	}
	response, _ = request(t, engine, http.MethodPost, "/api/manifests", operator, "snap-create-duplicate", manifestPayload("TM-SNAP-001", "CP-002"))
	assertStatus(t, response, http.StatusConflict)

	// 提交时冻结证照编号、状态、有效期和车辆数。
	response, body = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", manifest.ID), operator, "snap-submit", map[string]any{
		"status": "submitted", "expectedVersion": manifest.Version, "reason": "freeze qualification snapshot",
	})
	assertStatus(t, response, http.StatusOK)
	manifest = decodeManifestSnapshot(t, body)
	if manifest.Status != "submitted" || manifest.SnapshotVersion != 1 ||
		manifest.GeneratorPermitNumber != "PERMIT-WG-001" || manifest.GeneratorPermitStatus != "active" ||
		manifest.CarrierLicenseNumber != "CARRIER-LIC-002" || manifest.CarrierLicenseStatus != "verified" ||
		manifest.CarrierVehicleCount != 16 || manifest.SnapshotInvalidReason != "" {
		t.Fatalf("submit must freeze the qualification snapshot: %+v", manifest)
	}

	// 重复提交只能成功一次。
	response, _ = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", manifest.ID), operator, "snap-submit-again", map[string]any{
		"status": "submitted", "expectedVersion": manifest.Version, "reason": "duplicate submit must be rejected",
	})
	assertStatus(t, response, http.StatusUnprocessableEntity)

	// 并发提交同一草稿只能成功一次。
	response, body = request(t, engine, http.MethodPost, "/api/manifests", operator, "snap-create-concurrent", manifestPayload("TM-SNAP-002", "CP-002"))
	assertStatus(t, response, http.StatusCreated)
	concurrent := decodeManifestSnapshot(t, body)
	type transitionResult struct{ status int }
	results := make(chan transitionResult, 2)
	for i := 0; i < 2; i++ {
		go func() {
			res, _ := request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", concurrent.ID), operator, "", map[string]any{
				"status": "submitted", "expectedVersion": concurrent.Version, "reason": "concurrent submit",
			})
			results <- transitionResult{status: res.StatusCode}
		}()
	}
	first, second := <-results, <-results
	successes := 0
	for _, res := range []transitionResult{first, second} {
		if res.status == http.StatusOK {
			successes++
		} else if res.status != http.StatusConflict && res.status != http.StatusUnprocessableEntity {
			t.Fatalf("concurrent submit returned unexpected status %d", res.status)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent submits must succeed exactly once, got %d", successes)
	}

	// 刷新后可回读冻结快照。
	response, body = request(t, engine, http.MethodGet, fmt.Sprintf("/api/manifests/%d", concurrent.ID), reviewer, "", nil)
	assertStatus(t, response, http.StatusOK)
	if reread := decodeManifestSnapshot(t, body); reread.Status != "submitted" || reread.SnapshotVersion != 1 || reread.GeneratorPermitNumber != "PERMIT-WG-001" {
		t.Fatalf("snapshot must be re-readable after refresh: %+v", reread)
	}

	// 快照查询接口返回版本与有效期。
	response, body = request(t, engine, http.MethodGet, "/api/manifests/snapshots?codes=TM-SNAP-001,TM-SNAP-002", reviewer, "", nil)
	assertStatus(t, response, http.StatusOK)
	var snapshots struct {
		Data []struct {
			ManifestCode          string `json:"manifestCode"`
			SnapshotVersion       uint   `json:"snapshotVersion"`
			GeneratorPermitNumber string `json:"generatorPermitNumber"`
			InvalidReason         string `json:"invalidReason"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &snapshots); err != nil || len(snapshots.Data) != 2 {
		t.Fatalf("snapshot query must return both manifests: %v body=%s", err, string(body))
	}
	for _, snapshot := range snapshots.Data {
		if snapshot.SnapshotVersion != 1 || snapshot.GeneratorPermitNumber != "PERMIT-WG-001" || snapshot.InvalidReason != "" {
			t.Fatalf("unexpected snapshot payload: %+v", snapshot)
		}
	}

	// 停用产废许可：证照变化不得改写已冻结的历史快照。
	response, body = request(t, engine, http.MethodGet, "/api/generators?search=WG-001", reviewer, "", nil)
	assertStatus(t, response, http.StatusOK)
	var generatorPage struct {
		Data []record `json:"data"`
	}
	if err := json.Unmarshal(body, &generatorPage); err != nil || len(generatorPage.Data) != 1 {
		t.Fatalf("find generator WG-001: %v body=%s", err, string(body))
	}
	generator := generatorPage.Data[0]
	response, _ = request(t, engine, http.MethodPost, fmt.Sprintf("/api/generators/%d/transition", generator.ID), reviewer, "snap-suspend-generator", map[string]any{
		"status": "suspended", "expectedVersion": generator.Version, "reason": "permit suspended for snapshot test",
	})
	assertStatus(t, response, http.StatusOK)
	response, body = request(t, engine, http.MethodGet, fmt.Sprintf("/api/manifests/%d", manifest.ID), reviewer, "", nil)
	assertStatus(t, response, http.StatusOK)
	if frozen := decodeManifestSnapshot(t, body); frozen.GeneratorPermitStatus != "active" || frozen.GeneratorPermitNumber != "PERMIT-WG-001" || frozen.SnapshotVersion != 1 {
		t.Fatalf("certificate change must not rewrite the frozen snapshot: %+v", frozen)
	}

	// 核验决定只按冻结快照：实时停用不影响仍为有效的快照。
	response, body = request(t, engine, http.MethodPost, "/api/checks", operator, "snap-check-create", checkPayload("CC-SNAP-001", manifest.Code))
	assertStatus(t, response, http.StatusCreated)
	check := decodeRecord(t, body)
	response, _ = request(t, engine, http.MethodPost, fmt.Sprintf("/api/checks/%d/transition", check.ID), reviewer, "snap-check-pass", map[string]any{
		"status": "pass", "expectedVersion": check.Version, "reason": "frozen snapshot is still decision-grade",
	})
	assertStatus(t, response, http.StatusOK)

	// 发运前实时失效：整单拒绝且状态不变，失效原因随快照留存。
	response, _ = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", manifest.ID), operator, "snap-dispatch-blocked", map[string]any{
		"status": "in_transit", "expectedVersion": manifest.Version, "reason": "dispatch must be rejected while permit is suspended",
	})
	assertStatus(t, response, http.StatusUnprocessableEntity)
	response, body = request(t, engine, http.MethodGet, fmt.Sprintf("/api/manifests/%d", manifest.ID), reviewer, "", nil)
	assertStatus(t, response, http.StatusOK)
	manifest = decodeManifestSnapshot(t, body)
	if manifest.Status != "submitted" || manifest.SnapshotVersion != 1 || !strings.Contains(manifest.SnapshotInvalidReason, "产废许可") {
		t.Fatalf("blocked dispatch must keep status and record the invalid reason: %+v", manifest)
	}

	// 携带失效原因的快照必须拦截核验通过。
	response, body = request(t, engine, http.MethodPost, "/api/checks", operator, "snap-check-blocked", checkPayload("CC-SNAP-002", manifest.Code))
	assertStatus(t, response, http.StatusCreated)
	blockedCheck := decodeRecord(t, body)
	response, _ = request(t, engine, http.MethodPost, fmt.Sprintf("/api/checks/%d/transition", blockedCheck.ID), reviewer, "snap-check-blocked-pass", map[string]any{
		"status": "pass", "expectedVersion": blockedCheck.Version, "reason": "snapshot invalid reason must block the pass",
	})
	assertStatus(t, response, http.StatusUnprocessableEntity)

	// 恢复许可后发运成功，快照版本递增且失效原因清空。
	response, body = request(t, engine, http.MethodGet, "/api/generators?search=WG-001", reviewer, "", nil)
	assertStatus(t, response, http.StatusOK)
	if err := json.Unmarshal(body, &generatorPage); err != nil || len(generatorPage.Data) != 1 {
		t.Fatalf("re-find generator WG-001: %v body=%s", err, string(body))
	}
	generator = generatorPage.Data[0]
	response, _ = request(t, engine, http.MethodPost, fmt.Sprintf("/api/generators/%d/transition", generator.ID), reviewer, "snap-restore-generator", map[string]any{
		"status": "active", "expectedVersion": generator.Version, "reason": "permit reinstated",
	})
	assertStatus(t, response, http.StatusOK)
	response, body = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", manifest.ID), operator, "snap-dispatch", map[string]any{
		"status": "in_transit", "expectedVersion": manifest.Version, "reason": "permits verified again before dispatch",
	})
	assertStatus(t, response, http.StatusOK)
	manifest = decodeManifestSnapshot(t, body)
	if manifest.Status != "in_transit" || manifest.SnapshotVersion != 2 || manifest.SnapshotInvalidReason != "" {
		t.Fatalf("dispatch must refreeze the snapshot and clear the invalid reason: %+v", manifest)
	}

	// 快照恢复有效后核验可通过。
	response, body = request(t, engine, http.MethodPost, "/api/checks", operator, "snap-check-final", checkPayload("CC-SNAP-003", manifest.Code))
	assertStatus(t, response, http.StatusCreated)
	finalCheck := decodeRecord(t, body)
	response, _ = request(t, engine, http.MethodPost, fmt.Sprintf("/api/checks/%d/transition", finalCheck.ID), reviewer, "snap-check-final-pass", map[string]any{
		"status": "pass", "expectedVersion": finalCheck.Version, "reason": "refrozen snapshot is valid",
	})
	assertStatus(t, response, http.StatusOK)

	// 发运拒绝已随请求 ID 审计。
	response, body = request(t, engine, http.MethodGet, "/api/audits?search=dispatch_blocked", reviewer, "", nil)
	assertStatus(t, response, http.StatusOK)
	if !bytes.Contains(body, []byte("snap-dispatch-blocked")) {
		t.Fatalf("dispatch block must be audited with its request id: %s", string(body))
	}
}

func TestRBACLinkedComplianceWorkflowAndAuditing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := testConfig(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, redisClient, err := database.Open(context.Background(), cfg, logger)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if redisClient != nil {
		t.Fatal("test must use in-memory limiter without Redis")
	}
	engine := router.New(cfg, db, nil, logger)

	viewer := login(t, engine, "viewer")
	operator := login(t, engine, "operator")
	reviewer := login(t, engine, "reviewer")

	response, _ := request(t, engine, http.MethodGet, "/api/generators?page=1&pageSize=10", viewer, "", nil)
	assertStatus(t, response, http.StatusOK)
	response, _ = request(t, engine, http.MethodPost, "/api/manifests", viewer, "viewer-write", manifestPayload("TM-VIEWER", "CP-002"))
	assertStatus(t, response, http.StatusForbidden)
	response, _ = request(t, engine, http.MethodGet, "/api/audits", viewer, "", nil)
	assertStatus(t, response, http.StatusForbidden)

	response, body := request(t, engine, http.MethodPost, "/api/manifests", operator, "manifest-create-valid", manifestPayload("TM-ROUTER-001", "CP-002"))
	assertStatus(t, response, http.StatusCreated)
	manifest := decodeRecord(t, body)
	if manifest.Status != "draft" || response.Header.Get("X-Request-ID") != "manifest-create-valid" {
		t.Fatalf("unexpected manifest response: %+v request-id=%q", manifest, response.Header.Get("X-Request-ID"))
	}

	response, _ = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", manifest.ID), operator, "manifest-skip", map[string]any{
		"status": "in_transit", "expectedVersion": manifest.Version, "reason": "must not skip submission",
	})
	assertStatus(t, response, http.StatusUnprocessableEntity)

	response, body = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", manifest.ID), operator, "manifest-submit", map[string]any{
		"status": "submitted", "expectedVersion": manifest.Version, "reason": "linked permits checked",
	})
	assertStatus(t, response, http.StatusOK)
	manifest = decodeRecord(t, body)
	if manifest.Status != "submitted" || manifest.Version != 2 {
		t.Fatalf("manifest transition was not persisted: %+v", manifest)
	}

	response, _ = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", manifest.ID), operator, "manifest-stale", map[string]any{
		"status": "in_transit", "expectedVersion": uint(1), "reason": "stale client must conflict",
	})
	assertStatus(t, response, http.StatusConflict)

	response, body = request(t, engine, http.MethodPost, "/api/manifests", operator, "manifest-create-unverified", manifestPayload("TM-ROUTER-002", "CP-001"))
	assertStatus(t, response, http.StatusCreated)
	unverified := decodeRecord(t, body)
	response, _ = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", unverified.ID), operator, "manifest-block-unverified", map[string]any{
		"status": "submitted", "expectedVersion": unverified.Version, "reason": "must verify carrier first",
	})
	assertStatus(t, response, http.StatusUnprocessableEntity)

	response, body = request(t, engine, http.MethodPost, "/api/manifests", operator, "manifest-create-rejected", manifestPayload("TM-ROUTER-003", "CP-002"))
	assertStatus(t, response, http.StatusCreated)
	rejected := decodeRecord(t, body)
	response, body = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", rejected.ID), operator, "manifest-submit-rejected", map[string]any{
		"status": "submitted", "expectedVersion": rejected.Version, "reason": "linked permits checked",
	})
	assertStatus(t, response, http.StatusOK)
	rejected = decodeRecord(t, body)
	response, _ = request(t, engine, http.MethodPost, fmt.Sprintf("/api/manifests/%d/transition", rejected.ID), operator, "manifest-reject", map[string]any{
		"status": "rejected", "expectedVersion": rejected.Version, "reason": "destination permit mismatch",
	})
	assertStatus(t, response, http.StatusOK)
	response, body = request(t, engine, http.MethodPost, "/api/checks", operator, "check-create-rejected", checkPayload("CC-ROUTER-002", rejected.Code))
	assertStatus(t, response, http.StatusCreated)
	rejectedCheck := decodeRecord(t, body)
	response, _ = request(t, engine, http.MethodPost, fmt.Sprintf("/api/checks/%d/transition", rejectedCheck.ID), reviewer, "rejected-manifest-pass", map[string]any{
		"status": "pass", "expectedVersion": rejectedCheck.Version, "reason": "a rejected manifest must not pass",
	})
	assertStatus(t, response, http.StatusUnprocessableEntity)

	response, body = request(t, engine, http.MethodPost, "/api/checks", operator, "check-create", checkPayload("CC-ROUTER-001", manifest.Code))
	assertStatus(t, response, http.StatusCreated)
	check := decodeRecord(t, body)
	response, _ = request(t, engine, http.MethodPost, fmt.Sprintf("/api/checks/%d/transition", check.ID), operator, "operator-decision", map[string]any{
		"status": "pass", "expectedVersion": check.Version, "reason": "operator must not decide",
	})
	assertStatus(t, response, http.StatusForbidden)

	response, body = request(t, engine, http.MethodPost, fmt.Sprintf("/api/checks/%d/transition", check.ID), reviewer, "reviewer-decision", map[string]any{
		"status": "pass", "expectedVersion": check.Version, "reason": "all four evidence groups verified",
	})
	assertStatus(t, response, http.StatusOK)
	check = decodeRecord(t, body)
	if check.Status != "pass" || check.Version != 2 {
		t.Fatalf("review decision was not persisted: %+v", check)
	}

	response, body = request(t, engine, http.MethodGet, "/api/audits?page=1&pageSize=100", reviewer, "audit-read", nil)
	assertStatus(t, response, http.StatusOK)
	if !bytes.Contains(body, []byte("manifest-submit")) || !bytes.Contains(body, []byte("reviewer-decision")) {
		t.Fatalf("expected request IDs in immutable audit list: %s", string(body))
	}
	var envelope apiEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Meta.Total < 5 {
		t.Fatalf("expected audited mutations, got total=%d error=%v", envelope.Meta.Total, err)
	}
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Config{
		AppName: "hazardous-waste-transfer-compliance-test", Environment: "test", Port: "0",
		DatabaseDriver: "sqlite", DatabaseDSN: "file:router-test?mode=memory&cache=shared&_pragma=busy_timeout(5000)",
		JWTSecret: "router-test-secret-at-least-32-characters", TokenTTL: time.Hour,
		RequestLimit: 10000, StartupTimeout: 5 * time.Second, ShutdownTimeout: 5 * time.Second,
		ReadHeaderTimeout: time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second,
	}
}

func login(t *testing.T, engine http.Handler, username string) string {
	t.Helper()
	response, body := request(t, engine, http.MethodPost, "/api/auth/login", "", "", map[string]any{
		"username": username, "password": "Admin123!",
	})
	assertStatus(t, response, http.StatusOK)
	var envelope struct {
		Data struct {
			Token string `json:"token"`
			Role  string `json:"role"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Data.Token == "" || envelope.Data.Role != username {
		t.Fatalf("login %s failed: role=%q error=%v body=%s", username, envelope.Data.Role, err, string(body))
	}
	return envelope.Data.Token
}

func request(t *testing.T, engine http.Handler, method, path, token, requestID string, payload any) (*http.Response, []byte) {
	t.Helper()
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("encode request: %v", err)
		}
		body = bytes.NewReader(encoded)
	}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, body)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if requestID != "" {
		req.Header.Set("X-Request-ID", requestID)
	}
	engine.ServeHTTP(recorder, req)
	return recorder.Result(), recorder.Body.Bytes()
}

func assertStatus(t *testing.T, response *http.Response, expected int) {
	t.Helper()
	if response.StatusCode != expected {
		t.Fatalf("expected HTTP %d, got %d", expected, response.StatusCode)
	}
}

func decodeRecord(t *testing.T, body []byte) record {
	t.Helper()
	var envelope struct {
		Data record `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Data.ID == 0 {
		t.Fatalf("decode record: %v body=%s", err, string(body))
	}
	return envelope.Data
}

func manifestPayload(code, carrier string) map[string]any {
	return map[string]any{
		"code": code, "name": "路由集成测试联单", "description": "valid linked transfer manifest",
		"generatorCode": "WG-001", "carrierCode": carrier, "wasteCode": "HW08-900-249-08", "quantityKg": 680.5,
		"destination": "合规处置中心 A", "facility": "东区危废暂存区", "owner": "operator", "category": "危废转运",
		"riskLevel": "medium", "metricValue": 68, "metricUnit": "score", "effectiveAt": time.Now().UTC().Format(time.RFC3339),
		"evidence": "minio://evidence/tests/manifest.pdf", "relatedCode": strings.ReplaceAll(code, "TM", "REL"),
	}
}

func checkPayload(code, manifest string) map[string]any {
	return map[string]any{
		"code": code, "name": "路由集成测试核验", "description": "linked compliance decision",
		"manifestCode": manifest, "checklist": "产废许可、承运资质、联单数量、处置去向", "decisionBasis": "",
		"facility": "复核中心", "owner": "reviewer", "category": "联单复核", "riskLevel": "medium",
		"metricValue": 92, "metricUnit": "score", "effectiveAt": time.Now().UTC().Format(time.RFC3339),
		"evidence": "minio://evidence/tests/check.pdf", "relatedCode": manifest,
	}
}
