import { request } from './client';

/**
 * Billing/usage contract of ai-portal-go (internal/routes/billing.go).
 * All endpoints live under /api/billing and require auth.
 * Money: `cost` / `*_quota` fields are raw micro-USD; `*_usd` fields are
 * display strings like "$1.234567" (or floats where noted).
 */

export type RangeKey = '1d' | '7d' | '15d' | '30d' | 'quarter';

export interface Balance {
  balance_usd: string;
  balance_quota: number;
  gift_balance_usd: string;
  gift_balance_quota: number;
  total_balance_usd: string;
  total_balance_quota: number;
}

export async function getBalance(): Promise<Balance> {
  return request<Balance>('/api/billing/balance');
}

export interface UsageSummaryDaily {
  date: string;
  requests: number;
  tokens: number;
  cost: number;
  cost_usd: string;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
}

export interface UsageSummary {
  daily: UsageSummaryDaily[];
  total_requests: number;
  total_tokens: number;
  total_cost: number;
  total_cost_usd: string;
  period_days: number;
}

export async function getUsageSummary(days: number): Promise<UsageSummary> {
  return request<UsageSummary>(`/api/billing/usage-summary?days=${days}`);
}

export interface UsageTrendPoint {
  date: string;
  time?: string;
  requests: number;
  tokens: number;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  cost: number;
  cost_usd: string;
}

export async function getUsageTrend(range: RangeKey): Promise<UsageTrendPoint[]> {
  const res = await request<{ daily: UsageTrendPoint[] }>(`/api/billing/usage-trend?range=${range}`);
  return res.daily ?? [];
}

export interface UsageByModelRow {
  model: string;
  requests: number;
  tokens: number;
  /** raw micro-USD */
  cost: number;
  avg_latency_ms: number;
  avg_tps: number;
}

export async function getUsageByModel(range: RangeKey): Promise<UsageByModelRow[]> {
  const res = await request<{ data: UsageByModelRow[] }>(`/api/billing/usage-by-model?range=${range}`);
  return res.data ?? [];
}

export interface UsageRecord {
  trace_id: string;
  model: string;
  provider?: string;
  input_tokens: number;
  output_tokens: number;
  cached_tokens?: number;
  request_count?: number;
  /** USD float on this endpoint (unlike /requests where it is raw quota) */
  total_cost: number;
  total_cost_usd?: string;
  latency_ms?: number;
  created_at: string;
  endpoint?: string;
  is_streaming?: boolean;
  status: string;
  group_id?: string;
  group_name?: string;
  api_key_name?: string;
  key_prefix?: string;
  client_ip?: string;
  user_agent?: string;
}

export interface Paged<T> {
  records: T[];
  total: number;
  page: number;
  page_size: number;
  total_pages: number;
}

export interface UsageRecordsQuery {
  page?: number;
  page_size?: number;
  start_date?: string;
  end_date?: string;
  api_key_id?: string;
  model?: string;
  status?: string;
  /** trace / request id — backend support to be confirmed in ai-portal-go */
  trace_id?: string;
}

export function usageRecordsQueryString(q: UsageRecordsQuery): string {
  const params = new URLSearchParams();
  if (q.page) params.set('page', String(q.page));
  if (q.page_size) params.set('page_size', String(q.page_size));
  if (q.start_date) params.set('start_date', q.start_date);
  if (q.end_date) params.set('end_date', q.end_date);
  if (q.api_key_id) params.set('api_key_id', q.api_key_id);
  if (q.model) params.set('model', q.model);
  if (q.status) params.set('status', q.status);
  if (q.trace_id) params.set('trace_id', q.trace_id);
  const s = params.toString();
  return s ? `?${s}` : '';
}

export async function getUsageRecords(q: UsageRecordsQuery): Promise<Paged<UsageRecord>> {
  return request<Paged<UsageRecord>>(`/api/billing/usage-records${usageRecordsQueryString(q)}`);
}

/** Total count of successful requests in range (status filter + tiny page). */
export async function getSuccessfulRequestsCount(startDate?: string, endDate?: string): Promise<number> {
  const q: UsageRecordsQuery = { page: 1, page_size: 1, status: 'success' };
  if (startDate) q.start_date = startDate;
  if (endDate) q.end_date = endDate;
  const res = await getUsageRecords(q);
  return res.total;
}

export interface BalanceTransaction {
  created_at: string;
  type: string;
  balance_type: string;
  amount_micro: number;
  amount_usd: number;
  balance_after: number;
  description: string;
}

export async function getBalanceTransactions(includeUsage = false): Promise<BalanceTransaction[]> {
  const res = await request<{ transactions: BalanceTransaction[] }>(
    `/api/billing/balance-transactions?include_usage=${includeUsage ? 1 : 0}`
  );
  return res.transactions ?? [];
}

export interface RechargeRecord {
  id: string;
  order_id: string;
  amount: number;
  currency: string;
  exchange_rate: number;
  status: string;
  payment_method: string;
  product_type: string;
  created_at: string;
  updated_at: string;
  has_receipt: boolean;
}

export async function getRechargeRecords(q: {
  page?: number;
  page_size?: number;
  status?: string;
}): Promise<Paged<RechargeRecord>> {
  const params = new URLSearchParams();
  if (q.page) params.set('page', String(q.page));
  if (q.page_size) params.set('page_size', String(q.page_size));
  if (q.status) params.set('status', q.status);
  const s = params.toString();
  return request<Paged<RechargeRecord>>(`/api/billing/recharge-records${s ? `?${s}` : ''}`);
}

/* ---------- formatting helpers ---------- */

/** "$1.234567" / 1234567 (micro) / 1.23 (usd float) -> display "$1.23". */
export function formatUSD(v: string | number | null | undefined): string {
  if (v === null || v === undefined) return '$0.00';
  let n: number;
  if (typeof v === 'string') {
    const parsed = parseFloat(v.replace(/[$,]/g, ''));
    n = Number.isNaN(parsed) ? 0 : parsed;
  } else if (Math.abs(v) >= 1_000_000 && Number.isInteger(v)) {
    // raw micro-USD quota
    n = v / 1_000_000;
  } else {
    n = v;
  }
  const digits = Math.abs(n) < 0.01 && n !== 0 ? 4 : 2;
  return `$${n.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: digits })}`;
}

/** micro-USD quota number -> float USD. */
export function quotaToUSD(v: number): number {
  return v / 1_000_000;
}

export function formatNumber(v: number | null | undefined): string {
  return (v ?? 0).toLocaleString('en-US');
}

/** Average per minute over N days, TR-style (3 significant decimals max). */
export function formatRate(v: number): string {
  if (!Number.isFinite(v)) return '0';
  return v.toLocaleString('en-US', { maximumFractionDigits: 3 });
}

export function formatDateTime(iso: string | null | undefined): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

export function formatDate(iso: string | null | undefined): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}
