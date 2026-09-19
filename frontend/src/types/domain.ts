
export interface QualificationSnapshot {
  id: number;
  manifestId: number;
  manifestCode: string;
  stage: 'submission' | 'dispatch' | string;
  version: number;
  valid: boolean;
  invalidReason?: string;
  generatorCode: string;
  generatorStatus: string;
  permitNumber: string;
  permitVersion: number;
  permitExpiresAt: string;
  carrierCode: string;
  carrierStatus: string;
  licenseNumber: string;
  licenseVersion: number;
  licenseExpiresAt: string;
  vehicleCount: number;
  frozenAt: string;
}

export interface DomainRecord {
  id: number;
  code: string;
  name: string;
  status: string;
  version: number;
  description: string;
  facility: string;
  owner: string;
  category: string;
  riskLevel: 'low' | 'medium' | 'high' | 'critical';
  metricValue: number;
  metricUnit: string;
  effectiveAt: string;
  evidence: string;
  relatedCode: string;
	permitNumber?: string;
	permitExpiresAt?: string;
	wasteCategories?: string;
	licenseNumber?: string;
	licenseExpiresAt?: string;
	vehicleCount?: number;
	generatorCode?: string;
	carrierCode?: string;
	wasteCode?: string;
	quantityKg?: number;
	destination?: string;
	manifestCode?: string;
	checklist?: string;
	decisionBasis?: string;
  latestSnapshot?: QualificationSnapshot | null;
  frozenSnapshot?: QualificationSnapshot | null;
  createdAt: string;
  updatedAt: string;
}

export interface PageMeta { page: number; pageSize: number; total: number }
export interface ApiEnvelope<T> { data: T; error?: string; message?: string; meta?: PageMeta }
export type UserRole = 'viewer' | 'operator' | 'reviewer' | 'admin';
export interface UserSession { token: string; username: string; displayName: string; role: UserRole; expiresIn: number; requestId?: string }
export interface AuditLog {
  id: number; requestId: string; actor: string; action: string; entityType: string;
  entityId: number; beforeState: string; afterState: string; detail: string; createdAt: string;
}
export interface EntityConfig { key: string; path: string; label: string; statuses: readonly string[]; transitionRole: 'operator' | 'reviewer' }
