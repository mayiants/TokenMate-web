import { download, request } from './client';

/**
 * System-admin security & compliance surface (P3-A3).
 *
 * Sources:
 *  - audit logs:  internal/routes/audit.go (/api/admin/audit-logs)
 *  - retention:   internal/routes/settings_retention.go (/api/admin/settings/retention-defaults)
 *  - GDPR:        internal/routes/gdpr.go (/api/admin/export-requests, /api/admin/pending-deletions)
 */

// --- audit logs -------------------------------------------------------------

export interface AdminAuditLog {
  id: number;
  user_id?: string;
  user_email?: string;
  category?: string;
  action?: string;
  level?: string;
  message?: string;
  target_type?: string | null;
  target_id?: string | null;
  old_value?: string | null;
  new_value?: string | null;
  ip_address?: string | null;
  created_at?: string;
}

export interface AdminAuditLogDetail extends AdminAuditLog {
  user_agent?: string | null;
  details?: string | null;
}

export interface AdminAuditPage {
  logs: AdminAuditLog[];
  total: number;
  page: number;
  limit: number;
  total_pages: number;
}

export interface AuditFilters {
  page?: number;
  limit?: number;
  category?: string;
  action?: string;
  level?: string;
  start_date?: string;
  end_date?: string;
}

function auditQS(filters: AuditFilters): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(filters)) {
    if (v !== undefined && v !== '') q.set(k, String(v));
  }
  return q.toString();
}

export async function listAdminAuditLogs(filters: AuditFilters = {}): Promise<AdminAuditPage> {
  const res = await request<AdminAuditPage>(`/api/admin/audit-logs?${auditQS(filters)}`);
  return {
    logs: res.logs ?? [],
    total: res.total ?? 0,
    page: res.page ?? 1,
    limit: res.limit ?? 20,
    total_pages: res.total_pages ?? 1,
  };
}

export function getAdminAuditLog(id: number): Promise<AdminAuditLogDetail> {
  return request<AdminAuditLogDetail>(`/api/admin/audit-logs/${id}`);
}

export function exportAdminAuditLogs(filters: AuditFilters = {}): Promise<void> {
  const { page: _page, limit: _limit, ...rest } = filters;
  return download(`/api/admin/audit-logs/export?${auditQS(rest)}`);
}

// --- compliance: retention policy ------------------------------------------

export type RetentionMode = 'off' | 'metadata' | 'standard';

export interface RetentionDefaults {
  mode?: RetentionMode | string;
  days?: number;
  min_days?: number;
  max_days?: number;
}

export function getRetentionDefaults(): Promise<RetentionDefaults> {
  return request<RetentionDefaults>('/api/admin/settings/retention-defaults');
}

// --- compliance: GDPR export requests --------------------------------------

export interface GdprExportRequest {
  id: string;
  user_id?: string;
  user_email?: string;
  email?: string;
  scope?: string;
  status?: string;
  requested_at?: string;
  processed_at?: string | null;
}

export async function listGdprExportRequests(status?: string): Promise<GdprExportRequest[]> {
  const qs = status ? `?status=${encodeURIComponent(status)}` : '';
  const res = await request<{ requests?: GdprExportRequest[] }>(
    `/api/admin/export-requests${qs}`
  );
  return res.requests ?? [];
}

/** Generates the full export file and emails the requester. */
export async function processGdprExportRequest(id: string): Promise<void> {
  await request(`/api/admin/export-requests/${id}/process`, { method: 'POST' });
}

// --- compliance: pending deletions -----------------------------------------

export interface PendingDeletion {
  user_id: string;
  email?: string;
  name?: string;
  requested_at?: string;
  days_remaining?: number;
}

export interface PendingDeletions {
  retention_days?: number;
  pending_deletions: PendingDeletion[];
}

export async function listPendingDeletions(): Promise<PendingDeletions> {
  const res = await request<PendingDeletions>('/api/admin/pending-deletions');
  return {
    retention_days: res.retention_days,
    pending_deletions: res.pending_deletions ?? [],
  };
}

/** Skips the remaining retention window and anonymises immediately. */
export async function anonymizePendingDeletion(userId: string): Promise<void> {
  await request(`/api/admin/pending-deletions/${userId}/anonymize`, { method: 'POST' });
}
