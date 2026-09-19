package model

import "time"

// Qualification freezes the generator permit and carrier license values that a
// 转运联单 decision is based on. Snapshots are append-only: once frozen they are
// never updated or deleted, so later license changes cannot rewrite history.
//
// A snapshot is written for every 联单提交 (stage=submission) and every 发运
// attempt (stage=dispatch). 提交失败（资质当时即不合法）不产生快照；发运前实时
// 失效则写入 valid=false 的快照并冻结失效原因，联单状态保持不变。核验页只依据
// 这些冻结字段作出决定，不再读取证照实时数据。
type QualificationSnapshot struct {
	ID           uint   `json:"id" gorm:"primaryKey"`
	ManifestID   uint   `json:"manifestId" gorm:"uniqueIndex:uq_snapshot_manifest_version,priority:1;not null"`
	ManifestCode string `json:"manifestCode" gorm:"size:64;index;not null"`
	Stage        string `json:"stage" gorm:"size:24;index;not null"`
	Version      uint   `json:"version" gorm:"uniqueIndex:uq_snapshot_manifest_version,priority:2;not null"`
	// Valid uses a pointer so GORM always writes the evaluated value: a plain
	// bool with a DEFAULT would treat false as the zero value and substitute the
	// column default on INSERT, silently turning a rejected dispatch into valid.
	Valid            *bool     `json:"valid" gorm:"not null;default:false"`
	InvalidReason    string    `json:"invalidReason" gorm:"size:500"`
	GeneratorCode    string    `json:"generatorCode" gorm:"size:64;not null"`
	GeneratorStatus  string    `json:"generatorStatus" gorm:"size:40;not null"`
	PermitNumber     string    `json:"permitNumber" gorm:"size:80;not null"`
	PermitVersion    uint      `json:"permitVersion" gorm:"not null"`
	PermitExpiresAt  time.Time `json:"permitExpiresAt" gorm:"not null"`
	CarrierCode      string    `json:"carrierCode" gorm:"size:64;not null"`
	CarrierStatus    string    `json:"carrierStatus" gorm:"size:40;not null"`
	LicenseNumber    string    `json:"licenseNumber" gorm:"size:80;not null"`
	LicenseVersion   uint      `json:"licenseVersion" gorm:"not null"`
	LicenseExpiresAt time.Time `json:"licenseExpiresAt" gorm:"not null"`
	VehicleCount     int       `json:"vehicleCount" gorm:"not null"`
	FrozenAt         time.Time `json:"frozenAt" gorm:"index;not null"`
}

func (QualificationSnapshot) TableName() string { return "qualification_snapshots" }

// IsValid reports whether the frozen snapshot validated both licenses. Rows are
// always created with Valid set, but the guard keeps hydration code safe.
func (s QualificationSnapshot) IsValid() bool { return s.Valid != nil && *s.Valid }

const (
	// SnapshotStageSubmission freezes 资质 when a manifest moves draft -> submitted.
	SnapshotStageSubmission = "submission"
	// SnapshotStageDispatch freezes 资质 when a manifest attempts submitted -> in_transit.
	SnapshotStageDispatch = "dispatch"
)
