import { request } from './client';

// Upstream management: providers, key pool, and health monitoring
// (internal/routes/admin_providers.go, admin_keypool.go, admin_upstream_ops.go,
// admin_usage.go). Every endpoint requires system admin.

// --- providers --------------------------------------------------------------

export interface AdminProvider {
  id: string;
  name: string;
  /** protocol family: international | domestic | aws | ... */
  type?: string;
  base_url?: string;
  sync_source?: string;
  /** masked (first4****) — never the raw key */
  api_key?: string | null;
  description?: string | null;
  enabled: boolean;
  sort_order?: number;
  key_count?: number;
}

export interface AdminProviderPayload {
  id?: string;
  name?: string;
  type?: string;
  base_url?: string;
  sync_source?: string;
  api_key?: string;
  description?: string;
  enabled?: boolean;
}

export function listAdminProviders(): Promise<AdminProvider[]> {
  return request<{ providers?: AdminProvider[] }>('/api/admin/providers').then(
    (res) => res.providers ?? []
  );
}

export async function createAdminProvider(payload: AdminProviderPayload): Promise<void> {
  await request('/api/admin/providers', { method: 'POST', body: payload });
}

/** API key is only written when non-empty; masked values are never sent back. */
export async function updateAdminProvider(
  id: string,
  payload: AdminProviderPayload
): Promise<void> {
  await request(`/api/admin/providers/${id}`, { method: 'PATCH', body: payload });
}

export async function toggleAdminProvider(id: string, enabled: boolean): Promise<void> {
  await request(`/api/admin/providers/${id}/toggle`, { method: 'POST', body: { enabled } });
}

export async function deleteAdminProvider(id: string): Promise<void> {
  await request(`/api/admin/providers/${id}`, { method: 'DELETE' });
}

/**
 * "Probe" a provider: the backend has no dedicated connection-test endpoint,
 * so we run the key-pool batch validator scoped to this provider — same
 * upstream /models call the proxy uses, results persisted on each key.
 */
export function probeAdminProvider(
  provider: string,
  limit = 5
): Promise<{ validated?: number; invalid?: number }> {
  return request('/api/admin/keypool/validate-batch', {
    method: 'POST',
    body: { provider, limit },
  });
}

// --- key pool ---------------------------------------------------------------

export interface AdminKey {
  id: string;
  provider: string;
  key_preview?: string;
  enabled: boolean;
  validation_status?: string | null;
  assignment_mode?: string;
  assigned_proxy?: string | null;
  tier?: string | null;
  base_url?: string | null;
  rpm_limit?: number | null;
  tpm_limit?: number | null;
  quota_limit?: number | null;
  models?: string[] | null;
  created_at?: string;
}

export interface AdminKeyPayload {
  provider?: string;
  key?: string;
  rpm_limit?: number;
  tpm_limit?: number;
  quota_limit?: number;
  tier?: string;
  base_url?: string;
  notes?: string;
}

export function listAdminKeys(): Promise<AdminKey[]> {
  return request<{ keys?: AdminKey[] }>('/api/admin/keypool').then((res) => res.keys ?? []);
}

export async function createAdminKey(payload: AdminKeyPayload): Promise<string> {
  const res = await request<{ key?: { id: string } }>('/api/admin/keypool', {
    method: 'POST',
    body: payload,
  });
  return res.key?.id ?? '';
}

/** Rotation = PUT with a new `key`; limits/tier can be adjusted at the same time. */
export async function updateAdminKey(id: string, payload: AdminKeyPayload): Promise<void> {
  await request(`/api/admin/keypool/${id}`, { method: 'PUT', body: payload });
}

export async function toggleAdminKey(id: string, enabled: boolean): Promise<void> {
  await request(`/api/admin/keypool/${id}`, { method: 'PATCH', body: { enabled } });
}

export async function validateAdminKey(
  id: string
): Promise<{ status?: string; models?: string[] }> {
  return request(`/api/admin/keypool/${id}/validate`, { method: 'POST' });
}

export async function deleteAdminKey(id: string): Promise<void> {
  await request(`/api/admin/keypool/${id}`, { method: 'DELETE' });
}

// --- monitoring -------------------------------------------------------------

export type MonitorPeriod = '24h' | '7d' | '15d' | '30d' | '90d';

export interface ProviderHealthRow {
  provider: string;
  model: string;
  request_count: number;
  success_count: number;
  failure_count: number;
  avg_latency_ms: number;
  availability: number;
  /** available | degraded | unavailable | unknown */
  status: string;
  last_bucket?: string;
}

export interface HealthSummary {
  available?: number;
  degraded?: number;
  unavailable?: number;
  unknown?: number;
  total?: number;
}

export async function listProviderHealth(
  period: MonitorPeriod = '24h'
): Promise<ProviderHealthRow[]> {
  const res = await request<{ data?: ProviderHealthRow[] }>(
    `/api/admin/upstream-ops/health?view=providers&period=${period}`
  );
  return res.data ?? [];
}

export async function getHealthSummary(period: MonitorPeriod = '24h'): Promise<HealthSummary> {
  const res = await request<{ summary?: HealthSummary }>(
    `/api/admin/upstream-ops/health/summary?period=${period}`
  );
  return res.summary ?? {};
}

export interface ErrorRow {
  model: string;
  group_id?: string;
  group_name?: string;
  failures: number;
  timeout: number;
  rate_limit: number;
  error_5xx: number;
  error_4xx: number;
  auth_error: number;
  network_error: number;
  response_timeout: number;
  content_mismatch: number;
  invalid_request: number;
  impacted_users: number;
  impacted_enterprises: number;
}

export async function listUpstreamErrors(period: MonitorPeriod = '24h'): Promise<ErrorRow[]> {
  const res = await request<{ data?: ErrorRow[] }>(
    `/api/admin/upstream-ops/errors?period=${period}`
  );
  return res.data ?? [];
}

export interface KeyMetric {
  id: string;
  provider: string;
  key_preview?: string;
  enabled: boolean;
  validation_status?: string | null;
  request_count: number;
  error_count: number;
  token_count: number;
  avg_latency_ms: number;
  quota_utilization?: number;
  health_status?: string;
}

export async function listKeyMetrics(period: MonitorPeriod = '24h'): Promise<KeyMetric[]> {
  const res = await request<{ data?: KeyMetric[] }>(
    `/api/admin/monitor-upstream/keys?page=1&page_size=1000&period=${period}`
  );
  return res.data ?? [];
}
