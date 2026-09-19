import { request } from './client';
import type { QualificationSnapshot } from '../types/domain';

export interface SnapshotHistoryResponse {
  manifestCode: string;
  latest: QualificationSnapshot | null;
  history: QualificationSnapshot[];
}

// Qualification snapshots are append-only; this endpoint only ever reads frozen
// rows, never live permit/license state.
export async function getSnapshotHistory(manifestCode: string) {
  return request<SnapshotHistoryResponse>(`/snapshots/${encodeURIComponent(manifestCode.toUpperCase())}`);
}
