import { request } from './client';

/**
 * Per-user upstream health (internal/routes/status.go, GET /api/status/upstream).
 * Rows are already scoped to the model/group pairs the caller can reach.
 * status values come from mapInternalStatus: available | degraded |
 * unavailable | unknown | missing.
 */
export type UpstreamPeriod = '90m' | '24h' | '7d' | '15d' | '30d' | '90d';
export type UpstreamStatusValue = 'available' | 'degraded' | 'unavailable' | 'unknown' | 'missing';

export interface UpstreamStatusRow {
  model: string;
  group_id: string;
  group_name: string;
  /** success percentage; -1 when there was no traffic */
  availability: number;
  avg_latency_ms: number;
  status: UpstreamStatusValue | string;
  request_count: number;
  success_count: number;
  failure_count: number;
  last_bucket?: string | null;
}

export interface UpstreamStatusResponse {
  meta: { period: string; updated_at: string };
  data: UpstreamStatusRow[];
}

export async function getUpstreamStatus(period: UpstreamPeriod = '24h'): Promise<UpstreamStatusResponse> {
  return request<UpstreamStatusResponse>(`/api/status/upstream?period=${period}`);
}
