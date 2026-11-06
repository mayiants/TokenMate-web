import { request } from './client';

/**
 * Platform groups and subscription plans (internal/routes/groups.go,
 * subscriptions_plans.go). Every endpoint is guarded by RequireAdmin().
 *
 * `models` is a jsonb array that pgx may serialise as a JSON string; callers
 * should pass it through normalizeModelList before rendering.
 */
export interface AdminGroup {
  id: string;
  name: string;
  description?: string | null;
  /** general | economy | subscription | enterprise */
  group_type: string;
  billing_mode?: string;
  pricing_ratio?: number;
  models?: string[] | string | null;
  /** legacy single-window limits */
  rate_limit_rpm?: number | null;
  rate_limit_tpm?: number | null;
  concurrent_limit?: number;
  status?: string;
  member_count?: number;
  key_count?: number;
  enterprise_name?: string;
  parent_name?: string;
  sync_mode?: string;
  missing_models?: number;
  stale_models?: number;
  created_at?: string;
  updated_at?: string;
}

export interface AdminGroupMember {
  id: string;
  name?: string;
  email?: string;
  role?: string;
}

export interface AdminGroupDetail extends AdminGroup {
  rpm?: number | null;
  rph?: number | null;
  rp5h?: number | null;
  rpw?: number | null;
  rpmonth?: number | null;
  tpm?: number | null;
  tph?: number | null;
  tpd?: number | null;
  tpw?: number | null;
  tpmonth?: number | null;
  members?: AdminGroupMember[];
  inherit_to_enterprises?: boolean;
}

export interface AdminGroupPayload {
  name?: string;
  description?: string;
  group_type?: string;
  rate_limit_rpm?: number;
  rate_limit_tpm?: number;
  /** update endpoint names these rpm / tpm */
  rpm?: number;
  tpm?: number;
  concurrent_limit?: number;
  models?: string[];
  status?: string;
}

/** jsonb arrays arrive as arrays or JSON strings depending on the driver path. */
export function normalizeModelList(raw: unknown): string[] {
  if (!raw) return [];
  if (Array.isArray(raw)) {
    return raw.filter((m): m is string => typeof m === 'string' && m.length > 0);
  }
  if (typeof raw === 'string') {
    try {
      const parsed: unknown = JSON.parse(raw);
      return Array.isArray(parsed)
        ? parsed.filter((m): m is string => typeof m === 'string' && m.length > 0)
        : [];
    } catch {
      return [];
    }
  }
  return [];
}

export async function listAdminGroups(): Promise<AdminGroup[]> {
  const res = await request<{ groups?: AdminGroup[] }>('/api/admin/groups');
  return res.groups ?? [];
}

export async function getAdminGroup(id: string): Promise<AdminGroupDetail> {
  const res = await request<{ group: AdminGroupDetail }>(`/api/admin/groups/${id}`);
  return res.group;
}

export async function createAdminGroup(payload: AdminGroupPayload): Promise<string> {
  const res = await request<{ group?: { id: string } }>('/api/admin/groups', {
    method: 'POST',
    body: payload,
  });
  return res.group?.id ?? '';
}

/**
 * The update handler only persists fields that are present, so the caller
 * passes exactly the edited keys (0 is a valid limit and survives).
 */
export async function updateAdminGroup(id: string, payload: AdminGroupPayload): Promise<void> {
  await request(`/api/admin/groups/${id}`, { method: 'PUT', body: payload });
}

export async function deleteAdminGroup(id: string): Promise<void> {
  await request(`/api/admin/groups/${id}`, { method: 'DELETE' });
}

// --- subscription plans -----------------------------------------------------

export interface AdminPlan {
  id: string;
  name: string;
  description?: string | null;
  /** micro-USD */
  price: number;
  /** micro-USD quota */
  quota: number;
  quota_limit?: number;
  daily_limit?: number;
  /** token | request */
  plan_type?: string;
  duration_days?: number;
  /** active | inactive */
  status?: string;
  created_at?: string;
  rpm_limit?: number;
  rph_limit?: number;
  rp5h_limit?: number;
  rpd_limit?: number;
  rpw_limit?: number;
  tpm_limit?: number;
  tph_limit?: number;
  tpd_limit?: number;
  tpw_limit?: number;
  concurrent_limit?: number;
  model_permissions?: string[] | string | null;
}

export interface AdminPlanPayload {
  name?: string;
  description?: string;
  price?: number;
  quota?: number;
  duration_days?: number;
  daily_limit?: number;
  plan_type?: string;
  status?: string;
  model_permissions?: string[] | null;
}

export async function listAdminPlans(): Promise<AdminPlan[]> {
  const res = await request<{ plans?: AdminPlan[] }>('/api/admin/subscriptions/plans');
  return res.plans ?? [];
}

export async function createAdminPlan(payload: AdminPlanPayload): Promise<string> {
  const res = await request<{ plan?: { id: string } }>('/api/admin/subscriptions/plans', {
    method: 'POST',
    body: payload,
  });
  return res.plan?.id ?? '';
}

export async function updateAdminPlan(id: string, payload: AdminPlanPayload): Promise<void> {
  await request(`/api/admin/subscriptions/plans/${id}`, { method: 'PUT', body: payload });
}

/** active <-> inactive; the server computes the next status. */
export async function toggleAdminPlan(id: string): Promise<void> {
  await request(`/api/admin/subscriptions/plans/${id}/toggle`, { method: 'POST' });
}

export async function deleteAdminPlan(id: string): Promise<void> {
  await request(`/api/admin/subscriptions/plans/${id}`, { method: 'DELETE' });
}

/** Active subscriber counts keyed by plan name (stats endpoint has no plan id). */
export async function getAdminPlanSubscribers(): Promise<Record<string, number>> {
  const res = await request<{
    byPlan?: { name: string; subscriber_count: number }[];
  }>('/api/admin/subscriptions/stats');
  const out: Record<string, number> = {};
  for (const row of res.byPlan ?? []) out[row.name] = row.subscriber_count;
  return out;
}
