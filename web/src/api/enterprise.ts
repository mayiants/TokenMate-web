import { request } from './client';

// Enterprise self-service console API surface (internal/routes/enterprise_*.go).
// All endpoints live under /api/enterprise and require enterprise membership;
// mutations additionally require the enterprise_admin role. Response shapes were
// verified live against the running portal on 2026-11-06.

export interface EnterpriseInfo {
  id: string;
  name: string;
  contact_email?: string;
  status?: string;
  country?: string;
  verified_at?: string | null;
  quota_total?: number;
  quota_used?: number;
  credit_total?: number;
  credit_used?: number;
  prepaid_balance?: number;
  pricing_ratio?: number;
  display_currency?: string;
  exchange_rate?: number;
  member_count?: number;
  created_at?: string;
  [key: string]: unknown;
}

export interface EnterpriseMember {
  id: string;
  email: string;
  name?: string;
  role?: string;
  status?: string;
  quota_limit?: number;
  quota_used?: number;
  credit_limit?: number;
  credit_used?: number;
  created_at?: string;
  [key: string]: unknown;
}

export interface EnterpriseInvitation {
  id: string;
  email?: string;
  role?: string;
  status?: string;
  created_at?: string;
  expires_at?: string;
  [key: string]: unknown;
}

export interface EnterpriseGroup {
  id: string;
  name?: string;
  rate_limit_rpm?: number;
  rate_limit_tpm?: number;
  models?: string[] | null;
  member_count?: number;
  key_count?: number;
  [key: string]: unknown;
}

export interface EnterpriseKey {
  id: string;
  name?: string;
  key_prefix?: string;
  status?: string;
  user_name?: string;
  user_email?: string;
  assigned_user_id?: string;
  key_type?: string;
  created_at?: string;
  expires_at?: string | null;
  model_routing_mode?: string;
  allowed_models?: string[] | null;
  group_id?: string;
  group_name?: string;
  [key: string]: unknown;
}

export interface UsageTrendPoint {
  date: string;
  tokens?: number;
  requests?: number;
  cost?: number;
}

export interface EnterpriseBillingDetails {
  billing_cycle?: string;
  credit?: {
    remaining?: number;
    total?: number;
    used?: number;
    remaining_usd?: number;
    total_usd?: number;
    used_usd?: number;
    [key: string]: unknown;
  };
  display_currency?: string;
  exchange_rate?: number;
  invoices?: unknown[] | null;
  next_reset_at?: string;
  quota_remaining?: number;
  [key: string]: unknown;
}

export interface ChangeRequest {
  id: string;
  type?: string;
  status?: string;
  payload?: unknown;
  created_at?: string;
  [key: string]: unknown;
}

// --- queries -----------------------------------------------------------------

export async function getMyEnterprise(): Promise<EnterpriseInfo> {
  const res = await request<{ enterprise: EnterpriseInfo }>('/api/enterprise');
  return res.enterprise;
}

export async function listMyEnterpriseMembers(): Promise<EnterpriseMember[]> {
  const res = await request<{ members?: EnterpriseMember[] }>('/api/enterprise/members');
  return res.members ?? [];
}

export async function listEnterpriseGroups(): Promise<EnterpriseGroup[]> {
  const res = await request<{ groups?: EnterpriseGroup[] }>('/api/enterprise/groups');
  return res.groups ?? [];
}

export async function listEnterpriseKeys(): Promise<EnterpriseKey[]> {
  const res = await request<{ keys?: EnterpriseKey[] }>('/api/enterprise/keys');
  return res.keys ?? [];
}

export async function getEnterpriseUsageTrend(): Promise<UsageTrendPoint[]> {
  const res = await request<{ trend?: UsageTrendPoint[] }>('/api/enterprise/usage/trend');
  return res.trend ?? [];
}

export async function getEnterpriseBillingDetails(): Promise<EnterpriseBillingDetails> {
  return request<EnterpriseBillingDetails>('/api/enterprise/billing/details');
}

export async function getEnterpriseBillingTrend(): Promise<UsageTrendPoint[]> {
  const res = await request<{ trend?: UsageTrendPoint[] }>('/api/enterprise/billing/trend');
  return res.trend ?? [];
}

export async function listEnterpriseInvitations(): Promise<EnterpriseInvitation[]> {
  const res = await request<{ invitations?: EnterpriseInvitation[] }>('/api/enterprise/invitations');
  return res.invitations ?? [];
}

export async function listMyChangeRequests(): Promise<ChangeRequest[]> {
  const res = await request<{ requests?: ChangeRequest[] | null }>(
    '/api/enterprise/settings/change-requests'
  );
  return res.requests ?? [];
}

export async function getEnterpriseAvailableModels(): Promise<string[]> {
  const res = await request<{ models?: string[] }>('/api/enterprise/available-models');
  return res.models ?? [];
}

export interface PagedUsageRecords {
  records?: Array<Record<string, unknown>>;
  total?: number;
  [key: string]: unknown;
}

export async function getEnterpriseUsageDetails(params: {
  page?: number;
  pageSize?: number;
  memberId?: string;
  keyId?: string;
}): Promise<PagedUsageRecords> {
  const q = new URLSearchParams();
  if (params.page) q.set('page', String(params.page));
  if (params.pageSize) q.set('page_size', String(params.pageSize));
  if (params.memberId) q.set('member_id', params.memberId);
  if (params.keyId) q.set('key_id', params.keyId);
  const qs = q.toString();
  return request<PagedUsageRecords>(`/api/enterprise/usage/details${qs ? '?' + qs : ''}`);
}

export async function getEnterpriseMemberLogs(params: {
  page?: number;
  pageSize?: number;
}): Promise<PagedUsageRecords> {
  const q = new URLSearchParams();
  if (params.page) q.set('page', String(params.page));
  if (params.pageSize) q.set('page_size', String(params.pageSize));
  return request<PagedUsageRecords>(`/api/enterprise/usage/member-logs?${q.toString()}`);
}

export async function getEnterpriseUsageErrors(): Promise<PagedUsageRecords> {
  return request<PagedUsageRecords>('/api/enterprise/usage/errors');
}

export async function getEnterpriseRateLimitHits(): Promise<PagedUsageRecords> {
  return request<PagedUsageRecords>('/api/enterprise/usage/rate-limits');
}

export interface EnterpriseReconciliation {
  members?: Array<Record<string, unknown>>;
  [key: string]: unknown;
}

export async function getEnterpriseReconciliation(): Promise<EnterpriseReconciliation> {
  return request<EnterpriseReconciliation>('/api/enterprise/reconciliation');
}

// --- mutations ----------------------------------------------------------------

export async function inviteEnterpriseMember(payload: {
  email: string;
  name?: string;
  role?: string;
}): Promise<void> {
  await request('/api/enterprise/members', { method: 'POST', body: payload });
}

export async function removeEnterpriseMember(userId: string): Promise<void> {
  await request(`/api/enterprise/members/${userId}`, { method: 'DELETE' });
}

export async function topupMemberQuota(userId: string, quota: number): Promise<void> {
  await request(`/api/enterprise/members/${userId}/topup`, { method: 'POST', body: { quota } });
}

export async function setMemberQuotaLimit(userId: string, limit: number): Promise<void> {
  await request(`/api/enterprise/members/${userId}/quota-limit`, {
    method: 'PUT',
    body: { quota_limit: limit },
  });
}

export async function setMemberCreditLimit(userId: string, limit: number): Promise<void> {
  await request(`/api/enterprise/members/${userId}/credit-limit`, {
    method: 'PUT',
    body: { credit_limit: limit },
  });
}

/** Server flips the status itself; toggling your own account is rejected (400). */
export async function toggleMemberStatus(userId: string): Promise<void> {
  await request(`/api/enterprise/members/${userId}/toggle`, { method: 'POST' });
}

export async function setMemberRole(userId: string, role: 'admin' | 'member'): Promise<void> {
  await request(`/api/enterprise/members/${userId}/role`, { method: 'PUT', body: { role } });
}

export async function updateMemberName(userId: string, name: string): Promise<void> {
  await request(`/api/enterprise/members/${userId}/name`, { method: 'PUT', body: { name } });
}

export interface EnterpriseGroupPayload {
  name?: string;
  /** Required on create: enterprise groups bind to a platform group. */
  platform_group_id?: string;
  models?: string[];
  rpm?: number;
  tpm?: number;
  concurrent_limit?: number;
  visibility?: string;
  status?: string;
  sync_mode?: string;
}

export async function createEnterpriseGroup(payload: EnterpriseGroupPayload): Promise<void> {
  await request('/api/enterprise/groups', { method: 'POST', body: payload });
}

export async function updateEnterpriseGroup(
  groupId: string,
  payload: EnterpriseGroupPayload
): Promise<void> {
  await request(`/api/enterprise/groups/${groupId}`, { method: 'PUT', body: payload });
}

export interface PlatformGroup {
  id: string;
  name?: string;
  rpm?: number;
  tpm?: number;
  pricing_ratio?: number;
  model_count?: number;
  [key: string]: unknown;
}

export async function getPlatformGroups(): Promise<PlatformGroup[]> {
  const res = await request<{ groups?: PlatformGroup[] }>('/api/enterprise/platform-groups');
  return res.groups ?? [];
}

export async function deleteEnterpriseGroup(groupId: string): Promise<void> {
  await request(`/api/enterprise/groups/${groupId}`, { method: 'DELETE' });
}

export async function updateEnterpriseProfile(payload: {
  name?: string;
  contact_email?: string;
}): Promise<void> {
  await request('/api/enterprise/profile', { method: 'PUT', body: payload });
}

export async function submitChangeRequest(payload: {
  type: string;
  payload: Record<string, unknown>;
}): Promise<void> {
  await request('/api/enterprise/settings/change-request', { method: 'POST', body: payload });
}

export async function reviewInvitation(invitationId: string, action: 'approve' | 'reject') {
  return request<{ link?: string }>(`/api/enterprise/invitations/${invitationId}/${action}`, {
    method: 'PUT',
  });
}

// --- enterprise keys (admin) ---------------------------------------------------

export interface EnterpriseKeyCreatePayload {
  name: string;
  /** Member user id the key is assigned to. */
  assigned_to: string;
  group_id?: string;
  models?: string[];
  model_routing_mode?: 'single' | 'round_robin' | 'priority';
  expires_in?: 0 | 30 | 90 | 365;
}

export async function createEnterpriseKey(
  payload: EnterpriseKeyCreatePayload
): Promise<{ key_id: string; key: string }> {
  return request('/api/enterprise/keys', { method: 'POST', body: payload });
}

export async function reassignEnterpriseKey(keyId: string, assignedTo: string): Promise<void> {
  await request(`/api/enterprise/keys/${keyId}`, {
    method: 'PUT',
    body: { assigned_to: assignedTo },
  });
}

export async function revokeEnterpriseKey(keyId: string): Promise<void> {
  await request(`/api/enterprise/keys/${keyId}/revoke`, { method: 'POST' });
}

export async function reactivateEnterpriseKey(keyId: string): Promise<void> {
  await request(`/api/enterprise/keys/${keyId}/reactivate`, { method: 'POST' });
}

export async function cancelInvitation(invitationId: string): Promise<void> {
  await request(`/api/enterprise/invitations/${invitationId}`, { method: 'DELETE' });
}

export async function resendInvitation(invitationId: string): Promise<void> {
  await request(`/api/enterprise/invitations/${invitationId}/resend`, { method: 'POST' });
}

// --- enterprise audit (admin) --------------------------------------------------
// GET /api/enterprise/audit returns two row sets: role changes from
// enterprise_member_role_log and unified events from enterprise_audit_log.
// The endpoint only filters by `category` and caps each set at 50 rows, so
// date/operator/change-type filtering and pagination happen client-side.

export interface EnterpriseRoleAudit {
  id: string;
  user_id?: string;
  old_role?: string;
  new_role?: string;
  changed_by?: string;
  changed_by_email?: string;
  changed_at: string;
  reason?: string;
}

export interface EnterpriseAuditEvent {
  id: string;
  category: string;
  change_type: string;
  field_name?: string;
  old_value?: number | null;
  new_value?: number | null;
  delta?: number | null;
  reason?: string;
  created_at: string;
  /** Operator email, or the literal 'system' when the writer had no user. */
  changed_by?: string;
  user_id?: string;
  user_email?: string;
  user_name?: string;
}

export async function getEnterpriseAudit(category?: string): Promise<{
  roles: EnterpriseRoleAudit[];
  events: EnterpriseAuditEvent[];
  category: string;
}> {
  const qs = category && category !== 'all' ? `?category=${encodeURIComponent(category)}` : '';
  const res = await request<{
    audit_log?: EnterpriseRoleAudit[];
    credit_log?: EnterpriseAuditEvent[];
    category?: string;
  }>(`/api/enterprise/audit${qs}`);
  return {
    roles: res.audit_log ?? [],
    events: res.credit_log ?? [],
    category: res.category ?? '',
  };
}

/** CSV export: type=credits honours category/from/to/operator/member/q; type=roles ignores them. */
export function enterpriseAuditExportPath(params: {
  type: 'credits' | 'roles';
  category?: string;
  from?: string;
  to?: string;
  operator?: string;
}): string {
  const q = new URLSearchParams({ type: params.type });
  if (params.category && params.category !== 'all') q.set('category', params.category);
  if (params.from) q.set('from', params.from);
  if (params.to) q.set('to', params.to);
  if (params.operator) q.set('operator', params.operator);
  return `/api/enterprise/audit/export?${q.toString()}`;
}

// --- enterprise alerts (admin) -------------------------------------------------
// Only the six platform-defined thresholds are configurable (enable/disable);
// there is no create/delete endpoint for rules.

export interface EnterpriseAlert {
  id: string;
  alert_type: string;
  threshold_percent: number;
  current_value?: number | null;
  limit_value?: number | null;
  notified_at: string;
  acknowledged_at?: string | null;
  acknowledged_by?: string | null;
}

export interface EnterpriseAlertSetting {
  alert_type: string;
  threshold_percent: number;
  enabled: boolean;
  configurable: boolean;
}

export async function listEnterpriseAlerts(params?: {
  status?: 'open' | 'acknowledged' | 'all';
  type?: string;
  limit?: number;
}): Promise<EnterpriseAlert[]> {
  const q = new URLSearchParams();
  if (params?.status && params.status !== 'all') q.set('status', params.status);
  if (params?.type && params.type !== 'all') q.set('type', params.type);
  if (params?.limit) q.set('limit', String(params.limit));
  const qs = q.toString();
  const res = await request<{ alerts?: EnterpriseAlert[] | null }>(
    `/api/enterprise/alerts${qs ? `?${qs}` : ''}`
  );
  return res.alerts ?? [];
}

export async function getEnterpriseAlertSettings(): Promise<EnterpriseAlertSetting[]> {
  const res = await request<{ settings?: EnterpriseAlertSetting[] | null }>(
    '/api/enterprise/alert-settings'
  );
  return res.settings ?? [];
}

export async function updateEnterpriseAlertSetting(payload: {
  alert_type: string;
  threshold_percent: number;
  enabled: boolean;
}): Promise<void> {
  await request('/api/enterprise/alert-settings', { method: 'PUT', body: payload });
}

export async function acknowledgeEnterpriseAlert(alertId: string): Promise<void> {
  await request(`/api/enterprise/alerts/${alertId}/ack`, { method: 'POST' });
}
