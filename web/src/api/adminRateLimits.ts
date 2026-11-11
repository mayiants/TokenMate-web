import { download, request } from './client';

/**
 * Admin rate-limit console — internal/routes/admin_usage.go.
 *   GET /api/admin/rate-limits/{usage,groups,apikeys,upstream-keys,capacity}?window=1h|24h|7d
 *   GET /api/admin/rate-limits/export?dimension=users|groups|apikeys|upstream&window=…
 *
 * `limits`/`used`/`utilization` are loosely typed key→value maps: the backend
 * only fills the windows that are actually configured (nil elsewhere).
 */
export type RateLimitWindow = '1h' | '24h' | '7d';
export type RateLimitDimension = 'users' | 'groups' | 'apikeys' | 'upstream';

type Num = number | null;

export interface RateLimitUser {
  id: string;
  name: string;
  email: string;
  role: string;
  request_count: number;
  total_tokens: number;
  total_cost: number;
  limits: Record<string, Num>;
  used: Record<string, Num>;
  resets: Record<string, string | null>;
  quota: { used: Num; remaining: Num };
  utilization: Record<string, number>;
  status: string;
}

export interface RateLimitUsageResponse {
  users: RateLimitUser[];
  summary: {
    total_requests: number;
    total_tokens: number;
    total_cost: number;
    period: string;
    total_users: number;
    limited_count: number;
  };
}

export interface RateLimitGroup {
  id: string;
  name: string;
  limits: Record<string, Num>;
  usage: { requests: number; tokens: number };
  utilization: Record<string, number>;
  status: string;
}

export interface RateLimitGroupsResponse {
  groups: RateLimitGroup[];
  summary: {
    total_groups: number;
    total_requests: number;
    total_tokens: number;
    period: string;
    limited_count: number;
  };
}

export interface RateLimitApiKey {
  id: string;
  name: string;
  key_prefix: string;
  status: string;
  key_type: string;
  limits: Record<string, Num>;
  group_limits: Record<string, Num>;
  effective_limits: Record<string, Num>;
  usage: { requests: number; tokens: number };
  utilization: Record<string, number>;
  item_status: string;
}

export interface RateLimitApiKeysResponse {
  api_keys: RateLimitApiKey[];
  summary: {
    total_keys: number;
    total_requests: number;
    total_tokens: number;
    period: string;
    limited_count: number;
  };
}

export interface RateLimitUpstreamKey {
  id: string;
  name: string;
  provider: string;
  enabled: boolean;
  limits: Record<string, Num>;
  usage: { requests: number; tokens: number };
  utilization: Record<string, number>;
  status: string;
}

export interface RateLimitUpstreamResponse {
  upstream_keys: RateLimitUpstreamKey[];
  summary: {
    total_keys: number;
    total_requests: number;
    total_tokens: number;
    period: string;
    limited_count: number;
  };
}

export interface RateLimitCapacity {
  upstream: {
    total_rpm: number;
    total_tpm: number;
    total_quota: number;
    quota_used: number;
    total_keys: number;
    enabled_keys: number;
  };
  downstream: {
    total_requests: number;
    total_tokens: number;
    peak_rpm: number;
    peak_tpm: number;
    avg_rpm: number;
    avg_tpm: number;
  };
  utilization: Record<string, number>;
  summary: { period: string; status: string; minutes: number };
}

export function getRateLimitUsage(window: RateLimitWindow): Promise<RateLimitUsageResponse> {
  return request<RateLimitUsageResponse>(`/api/admin/rate-limits/usage?window=${window}`);
}

export function getRateLimitGroups(window: RateLimitWindow): Promise<RateLimitGroupsResponse> {
  return request<RateLimitGroupsResponse>(`/api/admin/rate-limits/groups?window=${window}`);
}

export function getRateLimitApiKeys(window: RateLimitWindow): Promise<RateLimitApiKeysResponse> {
  return request<RateLimitApiKeysResponse>(`/api/admin/rate-limits/apikeys?window=${window}`);
}

export function getRateLimitUpstreamKeys(
  window: RateLimitWindow
): Promise<RateLimitUpstreamResponse> {
  return request<RateLimitUpstreamResponse>(`/api/admin/rate-limits/upstream-keys?window=${window}`);
}

export function getRateLimitCapacity(window: RateLimitWindow): Promise<RateLimitCapacity> {
  return request<RateLimitCapacity>(`/api/admin/rate-limits/capacity?window=${window}`);
}

export async function exportRateLimits(
  dimension: RateLimitDimension,
  window: RateLimitWindow
): Promise<void> {
  await download(`/api/admin/rate-limits/export?dimension=${dimension}&window=${window}`);
}
