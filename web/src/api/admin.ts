import { request } from './client';

// System-admin API surface (internal/routes/admin_*.go + enterprise_common.go).
// Every endpoint below sits under /api/admin and is guarded by RequireAdmin()
// (system_admin | super_admin) on the backend; enterprise_admins get a 403.

export interface AdminMode {
  mode?: string;
  limits?: Record<string, number>;
  current?: { systemAdmins?: number; superAdmins?: number };
  canPerformSensitiveOps?: boolean;
  userRole?: string;
}

export interface AdminStats {
  totalUsers?: number;
  activeKeys?: number;
  upstreamKeys?: number;
  totalRequests?: number;
  todayRequests?: number;
}

export interface AdminAnalytics {
  stats?: {
    totalUsers?: number;
    totalKeys?: number;
    activeKeys?: number;
    totalRequests?: number;
    totalTokens?: number;
    totalRevenue?: number;
  };
  today?: { requests?: number; tokens?: number; revenue?: number };
}

export interface AdminUsageChart {
  range?: string;
  labels?: string[];
  requests?: number[];
  tokens?: number[];
  cost?: number[];
}

export interface AdminTicketStats {
  stats?: Record<string, number>;
}

export interface AdminTicket {
  id: string;
  ticket_no?: number;
  subject?: string;
  category?: string;
  priority?: string;
  status?: string;
  requester_email?: string;
  updated_at?: string;
  created_at?: string;
}

export interface AdminUser {
  id: string;
  name?: string;
  email: string;
  role?: string;
  status?: string;
  balance?: number;
  gift_balance?: number;
  group_id?: string;
  group_name?: string;
  enterprise_id?: string;
  enterprise_name?: string;
  key_count?: number;
  created_at?: string;
  deleted?: boolean;
}

export interface AdminEnterprise {
  id: string;
  name: string;
  contact_email?: string;
  status?: string;
  quota_total?: number;
  quota_used?: number;
  credit_total?: number;
  credit_used?: number;
  prepaid_balance?: number;
  display_currency?: string;
  exchange_rate?: number;
  country?: string;
  tax_number?: string;
  member_count?: number;
  max_members?: number;
  max_admins?: number;
  created_at?: string;
  verified_at?: string | null;
}

export interface AdminChangeRequest {
  id: string;
  enterprise_id: string;
  enterprise_name?: string;
  requested_by?: string;
  requester_email?: string;
  field?: string;
  old_value?: string;
  new_value?: string;
  reason?: string;
  status?: string;
  created_at?: string;
}

export interface AdminInvitation {
  id: string;
  email?: string;
  role?: string;
  status?: string;
  enterprise_name?: string;
  expires_at?: string;
  created_at?: string;
}

export interface EnterpriseApplication {
  id: string;
  enterprise_name?: string;
  country?: string;
  contact_name?: string;
  contact_email?: string;
  registration_number?: string;
  status?: string;
  created_at?: string;
  applicant_name?: string;
  applicant_email?: string;
  rejection_count?: number;
}

// --- probes / dashboard ------------------------------------------------------

export function getAdminMode(): Promise<AdminMode> {
  return request<AdminMode>('/api/admin/admin-mode');
}

// Session-level probe cache: ConsoleLayout + AdminLayout both probe this on
// mount — without caching, every console page re-hits the endpoint (and every
// non-admin session logs a 403 per page). Cleared on login/logout.
let adminModeCache: Promise<AdminMode> | null = null;
export function getAdminModeCached(): Promise<AdminMode> {
  if (!adminModeCache) adminModeCache = getAdminMode();
  return adminModeCache;
}
export function clearAdminModeCache() {
  adminModeCache = null;
}

export function getAdminStats(): Promise<AdminStats> {
  return request<AdminStats>('/api/admin/stats');
}

export function getAdminAnalytics(range = '1d'): Promise<AdminAnalytics> {
  return request<AdminAnalytics>(`/api/admin/analytics?range=${encodeURIComponent(range)}`);
}

export function getAdminUsageChart(range = '30d'): Promise<AdminUsageChart> {
  return request<AdminUsageChart>(`/api/admin/usage/chart?range=${encodeURIComponent(range)}`);
}

export async function getAdminTicketStats(): Promise<Record<string, number>> {
  const res = await request<AdminTicketStats>('/api/admin/tickets/stats');
  return res.stats ?? {};
}

export async function listAdminTickets(status?: string): Promise<AdminTicket[]> {
  const qs = status ? `?status=${encodeURIComponent(status)}` : '';
  const res = await request<{ tickets?: AdminTicket[] }>(`/api/admin/tickets${qs}`);
  return res.tickets ?? [];
}

// --- users -------------------------------------------------------------------

export async function listAdminUsers(search?: string): Promise<AdminUser[]> {
  const qs = search ? `?search=${encodeURIComponent(search)}` : '';
  const res = await request<{ users?: AdminUser[] }>(`/api/admin/users${qs}`);
  return res.users ?? [];
}

export async function createAdminUser(payload: {
  name?: string;
  email: string;
  password: string;
  role?: string;
}): Promise<string> {
  const res = await request<{ user_id: string }>('/api/admin/users', {
    method: 'POST',
    body: payload,
  });
  return res.user_id;
}

export async function updateAdminUser(
  userId: string,
  payload: { name?: string; role?: string; confirm_role?: boolean }
): Promise<void> {
  await request(`/api/admin/users/${userId}`, { method: 'PATCH', body: payload });
}

/** Server flips active <-> suspended; suspending your own account is rejected. */
export async function toggleAdminUserStatus(userId: string): Promise<string> {
  const res = await request<{ status: string }>(`/api/admin/users/${userId}/toggle-status`, {
    method: 'POST',
  });
  return res.status;
}

export async function sendAdminPasswordReset(userId: string): Promise<void> {
  await request(`/api/admin/users/${userId}/send-password-reset`, { method: 'POST' });
}

export async function deleteAdminUser(userId: string): Promise<void> {
  await request(`/api/admin/users/${userId}`, { method: 'DELETE' });
}

// --- enterprises -------------------------------------------------------------

export async function listAdminEnterprises(): Promise<AdminEnterprise[]> {
  const res = await request<{ enterprises?: AdminEnterprise[] }>('/api/admin/enterprise');
  return res.enterprises ?? [];
}

export async function getAdminEnterprise(
  id: string
): Promise<{ enterprise: AdminEnterprise }> {
  return request<{ enterprise: AdminEnterprise }>(`/api/admin/enterprise/${id}`);
}

export async function createAdminEnterprise(payload: {
  name: string;
  contact_email?: string;
  quota_total?: number;
  credit_total?: number;
  max_members?: number;
}): Promise<string> {
  const res = await request<{ enterprise?: { id: string } }>('/api/admin/enterprise', {
    method: 'POST',
    body: payload,
  });
  return res.enterprise?.id ?? '';
}

export async function updateAdminEnterprise(
  id: string,
  payload: { name?: string; contact_email?: string; max_members?: number }
): Promise<void> {
  await request(`/api/admin/enterprise/${id}`, { method: 'PUT', body: payload });
}

export async function deleteAdminEnterprise(id: string): Promise<void> {
  await request(`/api/admin/enterprise/${id}`, { method: 'DELETE' });
}

export async function suspendAdminEnterprise(id: string): Promise<void> {
  await request(`/api/admin/enterprise/${id}/suspend`, { method: 'POST' });
}

export async function activateAdminEnterprise(id: string): Promise<void> {
  await request(`/api/admin/enterprise/${id}/activate`, { method: 'POST' });
}

export async function verifyAdminEnterprise(id: string): Promise<void> {
  await request(`/api/admin/enterprise/${id}/verify`, { method: 'POST' });
}

export interface TopupPayload {
  quota_total?: number;
  credit_total?: number;
  prepaid_balance?: number;
  reason?: string;
  issue_invoice?: boolean;
  invoice_title?: string;
}

export async function topupAdminEnterprise(id: string, payload: TopupPayload): Promise<void> {
  await request(`/api/admin/enterprise/${id}/topup`, { method: 'POST', body: payload });
}

export async function listAdminChangeRequests(status = 'pending'): Promise<AdminChangeRequest[]> {
  const res = await request<{ requests?: AdminChangeRequest[] }>(
    `/api/admin/enterprise/change-requests?status=${encodeURIComponent(status)}`
  );
  return res.requests ?? [];
}

export async function reviewAdminChangeRequest(
  id: string,
  action: 'approve' | 'reject'
): Promise<void> {
  await request(`/api/admin/enterprise/change-requests/${id}/review`, {
    method: 'POST',
    body: { action },
  });
}

export async function listAdminInvitations(): Promise<AdminInvitation[]> {
  const res = await request<{ invitations?: AdminInvitation[] }>(
    '/api/admin/enterprise/invitations'
  );
  return res.invitations ?? [];
}

export async function cancelAdminInvitation(id: string): Promise<void> {
  await request(`/api/admin/enterprise/invitations/${id}`, { method: 'DELETE' });
}

export async function listEnterpriseApplications(status?: string): Promise<EnterpriseApplication[]> {
  const qs = status ? `?status=${encodeURIComponent(status)}` : '';
  const res = await request<{ applications?: EnterpriseApplication[] }>(
    `/api/admin/enterprise-applications${qs}`
  );
  return res.applications ?? [];
}
