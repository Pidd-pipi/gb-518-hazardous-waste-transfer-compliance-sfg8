package model

import "time"

// TransferManifest models 转运清单 as an independently versioned aggregate. The fields
// cover ownership, operational context, evidence and measured risk so later
// changes naturally span persistence, service and UI layers.
type TransferManifest struct {
	BaseModel
	GeneratorCode string    `json:"generatorCode" gorm:"size:64;index;not null"`
	CarrierCode   string    `json:"carrierCode" gorm:"size:64;index;not null"`
	WasteCode     string    `json:"wasteCode" gorm:"size:64;index;not null"`
	QuantityKg    float64   `json:"quantityKg" gorm:"not null"`
	Destination   string    `json:"destination" gorm:"size:200;not null"`
	Facility      string    `json:"facility" gorm:"size:120;index"`
	Owner         string    `json:"owner" gorm:"size:120;index"`
	Category      string    `json:"category" gorm:"size:80;index"`
	RiskLevel     string    `json:"riskLevel" gorm:"size:32;index"`
	MetricValue   float64   `json:"metricValue"`
	MetricUnit    string    `json:"metricUnit" gorm:"size:24"`
	EffectiveAt   time.Time `json:"effectiveAt"`
	Evidence      string    `json:"evidence" gorm:"size:2000"`
	RelatedCode   string    `json:"relatedCode" gorm:"size:64;index"`
	// Qualification snapshot frozen when the manifest is submitted and again
	// when it is dispatched. Once frozen, later certificate changes on the
	// generator or carrier records must not rewrite these fields.
	SnapshotVersion          uint       `json:"snapshotVersion" gorm:"not null;default:0"`
	SnapshotAt               *time.Time `json:"snapshotAt"`
	GeneratorPermitNumber    string     `json:"generatorPermitNumber" gorm:"size:80"`
	GeneratorPermitStatus    string     `json:"generatorPermitStatus" gorm:"size:40"`
	GeneratorPermitExpiresAt *time.Time `json:"generatorPermitExpiresAt"`
	CarrierLicenseNumber     string     `json:"carrierLicenseNumber" gorm:"size:80"`
	CarrierLicenseStatus     string     `json:"carrierLicenseStatus" gorm:"size:40"`
	CarrierLicenseExpiresAt  *time.Time `json:"carrierLicenseExpiresAt"`
	CarrierVehicleCount      int        `json:"carrierVehicleCount"`
	SnapshotInvalidReason    string     `json:"snapshotInvalidReason" gorm:"size:500"`
}

func (item *TransferManifest) GetBase() *BaseModel { return &item.BaseModel }

func (item TransferManifest) TableName() string { return "transfer_manifests" }

var TransferManifestInitialStatus = "draft"
