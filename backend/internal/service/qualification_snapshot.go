package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/model"
)

// verifyLinkedQualifications validates the live generator permit and carrier
// license right before a manifest is submitted or dispatched. It returns an
// empty string when both parties are cleared, otherwise a human-readable
// rejection reason that is also persisted as the snapshot invalid reason.
func verifyLinkedQualifications(generator model.WasteGenerator, carrier model.CarrierProfile, now time.Time) string {
	switch {
	case generator.Status != "active":
		return fmt.Sprintf("产废许可已失效或停用（当前状态 %s）", generator.Status)
	case !generator.PermitExpiresAt.After(now):
		return fmt.Sprintf("产废许可 %s 已过有效期 %s", generator.PermitNumber, generator.PermitExpiresAt.Format("2006-01-02"))
	case carrier.Status != "verified":
		return fmt.Sprintf("承运资质已失效或停用（当前状态 %s）", carrier.Status)
	case !carrier.LicenseExpiresAt.After(now):
		return fmt.Sprintf("承运许可证 %s 已过有效期 %s", carrier.LicenseNumber, carrier.LicenseExpiresAt.Format("2006-01-02"))
	case carrier.VehicleCount <= 0:
		return "承运方没有有效车辆"
	}
	return ""
}

// freezeQualificationSnapshot copies the verified certificate number, status,
// validity and vehicle count onto the manifest and clears any previously
// recorded invalid reason. The snapshot version increases at every freeze so
// the verification page can tell which freeze it is looking at.
func freezeQualificationSnapshot(manifest *model.TransferManifest, generator model.WasteGenerator, carrier model.CarrierProfile, now time.Time) {
	permitExpiresAt := generator.PermitExpiresAt
	licenseExpiresAt := carrier.LicenseExpiresAt
	manifest.SnapshotVersion++
	manifest.SnapshotAt = &now
	manifest.GeneratorPermitNumber = generator.PermitNumber
	manifest.GeneratorPermitStatus = generator.Status
	manifest.GeneratorPermitExpiresAt = &permitExpiresAt
	manifest.CarrierLicenseNumber = carrier.LicenseNumber
	manifest.CarrierLicenseStatus = carrier.Status
	manifest.CarrierLicenseExpiresAt = &licenseExpiresAt
	manifest.CarrierVehicleCount = carrier.VehicleCount
	manifest.SnapshotInvalidReason = ""
}

// SnapshotInvalidReason evaluates only the qualification snapshot frozen with
// the manifest. It never reads the live generator or carrier records, so
// certificate changes after the freeze cannot rewrite history. An empty
// result means the frozen snapshot is still decision-grade.
func SnapshotInvalidReason(manifest model.TransferManifest, now time.Time) string {
	if manifest.SnapshotVersion == 0 || manifest.SnapshotAt == nil {
		return "联单尚未冻结资质快照"
	}
	if reason := strings.TrimSpace(manifest.SnapshotInvalidReason); reason != "" {
		return reason
	}
	if manifest.GeneratorPermitStatus != "active" {
		return fmt.Sprintf("快照内产废许可状态为 %s，不是 active", manifest.GeneratorPermitStatus)
	}
	if manifest.GeneratorPermitExpiresAt == nil || !manifest.GeneratorPermitExpiresAt.After(now) {
		return "快照内产废许可已过有效期"
	}
	if manifest.CarrierLicenseStatus != "verified" {
		return fmt.Sprintf("快照内承运资质状态为 %s，不是 verified", manifest.CarrierLicenseStatus)
	}
	if manifest.CarrierLicenseExpiresAt == nil || !manifest.CarrierLicenseExpiresAt.After(now) {
		return "快照内承运许可证已过有效期"
	}
	return ""
}
