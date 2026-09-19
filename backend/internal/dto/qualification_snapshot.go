package dto

import "time"

// ManifestSnapshot is the read model of the qualification snapshot frozen with
// a transfer manifest. The verification page uses it to display the snapshot
// version, validity period and invalid reason without touching live
// certificate records.
type ManifestSnapshot struct {
	ManifestCode             string     `json:"manifestCode"`
	ManifestStatus           string     `json:"manifestStatus"`
	SnapshotVersion          uint       `json:"snapshotVersion"`
	SnapshotAt               *time.Time `json:"snapshotAt"`
	GeneratorCode            string     `json:"generatorCode"`
	GeneratorPermitNumber    string     `json:"generatorPermitNumber"`
	GeneratorPermitStatus    string     `json:"generatorPermitStatus"`
	GeneratorPermitExpiresAt *time.Time `json:"generatorPermitExpiresAt"`
	CarrierCode              string     `json:"carrierCode"`
	CarrierLicenseNumber     string     `json:"carrierLicenseNumber"`
	CarrierLicenseStatus     string     `json:"carrierLicenseStatus"`
	CarrierLicenseExpiresAt  *time.Time `json:"carrierLicenseExpiresAt"`
	CarrierVehicleCount      int        `json:"carrierVehicleCount"`
	InvalidReason            string     `json:"invalidReason"`
}
