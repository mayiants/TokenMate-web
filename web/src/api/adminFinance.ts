import { download, request } from './client';

/**
 * System-admin finance surface (P3-A3).
 *
 * Sources:
 *  - subscriptions: internal/routes/subscriptions_common.go + subscriptions_subscriptions.go
 *  - vouchers:      internal/routes/vouchers_common.go + vouchers_admin.go
 *  - reconciliation: internal/routes/admin_reconciliation_ops.go
 *
 * Quota amounts are raw micro-USD integers; voucher `amount` is also stored as
 * quota. Subscription list endpoints cap at 20 rows server-side.
 */

// --- subscriptions ----------------------------------------------------------

export interface AdminUserSubscription {
  id: string;
  user_id: string;
  plan_id?: string | null;
  plan_name?: string;
  status?: string;
  user_name?: string;
  user_email?: string;
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
  quota_used?: number;
  quota_remaining?: number;
  daily_limit?: number;
  daily_quota_used?: number;
}

export interface AdminSubscriptionStats {
  overview?: {
    active_count?: number;
    expired_count?: number;
    cancelled_count?: number;
    total_revenue?: number;
  };
  byPlan?: { name: string; subscriber_count: number }[];
}

export async function listAdminUserSubscriptions(): Promise<AdminUserSubscription[]> {
  const res = await request<{ subscriptions?: AdminUserSubscription[] }>(
    '/api/admin/subscriptions/users'
  );
  return res.subscriptions ?? [];
}

export function getAdminSubscriptionStats(): Promise<AdminSubscriptionStats> {
  return request<AdminSubscriptionStats>('/api/admin/subscriptions/stats');
}

/** Only quota/limit fields are mutable; status changes have no admin endpoint. */
export interface AdminSubscriptionPatch {
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
  daily_limit?: number;
  quota_remaining?: number;
}

export async function updateAdminUserSubscription(
  id: string,
  payload: AdminSubscriptionPatch
): Promise<void> {
  await request(`/api/admin/subscriptions/users/${id}`, { method: 'PUT', body: payload });
}

// --- vouchers ---------------------------------------------------------------

export interface AdminVoucher {
  id: string;
  code: string;
  name?: string;
  /** gift | welcome | plan_token | referral */
  type?: string;
  owner_type?: string;
  /** micro-USD */
  amount: number;
  reward_type?: string;
  max_uses?: number;
  used_count?: number;
  status?: string;
  expires_at?: string | null;
  created_at?: string;
  description?: string;
  creator_name?: string;
  creator_email?: string;
}

export interface AdminVoucherPage {
  vouchers: AdminVoucher[];
  total: number;
  page: number;
  total_pages: number;
}

export interface AdminVoucherStats {
  vouchers?: {
    total?: number;
    active?: number;
    disabled?: number;
    exhausted?: number;
    total_redeemed_amount?: number;
  };
  uses?: { total?: number; total_amount?: number };
}

export async function listAdminVouchers(
  params: { page?: number; limit?: number; type?: string; status?: string } = {}
): Promise<AdminVoucherPage> {
  const q = new URLSearchParams();
  q.set('page', String(params.page ?? 1));
  q.set('limit', String(params.limit ?? 50));
  if (params.type && params.type !== 'all') q.set('type', params.type);
  if (params.status && params.status !== 'all') q.set('status', params.status);
  const res = await request<AdminVoucherPage>(`/api/admin/vouchers?${q.toString()}`);
  return {
    vouchers: res.vouchers ?? [],
    total: res.total ?? 0,
    page: res.page ?? 1,
    total_pages: res.total_pages ?? 1,
  };
}

export interface CreateVouchersPayload {
  name: string;
  /** gift | welcome | referral */
  type: string;
  /** USD face value; the server converts to quota */
  amount: number;
  count: number;
  max_uses: number;
  description?: string;
  /** YYYY-MM-DD */
  expires_at?: string;
}

export async function createAdminVouchers(
  payload: CreateVouchersPayload
): Promise<{ codes: string[]; count: number }> {
  const res = await request<{ codes?: string[]; count?: number }>('/api/admin/vouchers', {
    method: 'POST',
    body: payload,
  });
  return { codes: res.codes ?? [], count: res.count ?? res.codes?.length ?? 0 };
}

/** active <-> disabled; the server computes the next status. */
export async function toggleAdminVoucher(id: string): Promise<string> {
  const res = await request<{ status?: string }>(`/api/admin/vouchers/${id}/toggle`, {
    method: 'POST',
  });
  return res.status ?? '';
}

export async function updateAdminVoucher(
  id: string,
  payload: { name?: string; description?: string; expires_at?: string }
): Promise<void> {
  await request(`/api/admin/vouchers/${id}`, { method: 'PATCH', body: payload });
}

export function getAdminVoucherStats(): Promise<AdminVoucherStats> {
  return request<AdminVoucherStats>('/api/admin/vouchers/stats');
}

export interface AdminVoucherUse {
  id: string;
  code: string;
  type?: string;
  amount?: number;
  consumer_type?: string;
  reward_granted?: boolean;
  user_name?: string;
  user_email?: string;
  created_at?: string;
}

// --- upstream reconciliation ------------------------------------------------

export type ReconPeriod = '24h' | '7d' | '30d' | '90d';

export interface ReconciliationSummary {
  period?: string;
  date_from?: string;
  date_to?: string;
  upstream_requests?: number;
  upstream_input_tokens?: number;
  upstream_output_tokens?: number;
  upstream_errors?: number;
  billed_requests?: number;
  billed_input_tokens?: number;
  billed_output_tokens?: number;
  billed_cost_quota?: number;
  billed_cost_usd?: number;
  estimated_upstream_cost_usd?: number;
  cost_diff_usd?: number;
  gross_margin_usd?: number;
  gross_margin_pct?: number;
  error_rate?: number;
}

export interface ReconciliationRow {
  provider: string;
  model: string;
  upstream_model?: string;
  upstream_key_id?: string;
  key_preview?: string;
  upstream_requests?: number;
  billed_requests?: number;
  request_diff?: number;
  upstream_input_tokens?: number;
  billed_input_tokens?: number;
  upstream_output_tokens?: number;
  billed_output_tokens?: number;
  upstream_cost_usd?: number;
  billed_cost_usd?: number;
  cost_diff_usd?: number;
  gross_margin_usd?: number;
  gross_margin_pct?: number;
  error_count?: number;
  error_rate?: number;
}

export interface ReconciliationDetail {
  rows: ReconciliationRow[];
  total: number;
  page: number;
  limit: number;
  total_pages: number;
}

function reconQS(params: Record<string, string | number | undefined>): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== '') q.set(k, String(v));
  }
  return q.toString();
}

export function getReconciliationSummary(period: ReconPeriod): Promise<ReconciliationSummary> {
  return request<ReconciliationSummary>(
    `/api/admin/reconciliation-ops/summary?${reconQS({ period })}`
  );
}

export function getReconciliationDetail(params: {
  period: ReconPeriod;
  page?: number;
  limit?: number;
  search?: string;
  diff_threshold?: number;
}): Promise<ReconciliationDetail> {
  return request<ReconciliationDetail>(
    `/api/admin/reconciliation-ops/detail?${reconQS(params)}`
  );
}

export function exportReconciliation(params: {
  period: ReconPeriod;
  search?: string;
  diff_threshold?: number;
}): Promise<void> {
  return download(`/api/admin/reconciliation-ops/export?${reconQS(params)}`);
}
