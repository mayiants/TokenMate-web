import { download, request } from './client';

/**
 * Admin usage explorer — internal/routes/admin_usage_explorer.go.
 *   GET /api/admin/usage-explorer/aggregate?dimension=&period=|date_from&date_to&limit=
 *   GET /api/admin/usage-explorer/logs?dimension=&value=&period=&page=&limit=
 *   GET /api/admin/usage-explorer/export/{aggregate,logs}?…
 *
 * `total_cost` is raw quota (int64), not USD — convert with quotaToUSD().
 */
export type ExplorerDimension = 'user' | 'enterprise' | 'model' | 'key' | 'endpoint' | 'subscription_plan';

export const EXPLORER_DIMENSIONS: ExplorerDimension[] = [
  'user',
  'enterprise',
  'model',
  'key',
  'endpoint',
  'subscription_plan',
];

export type ExplorerPeriod = '24h' | '7d' | '15d' | '30d' | '90d';

export interface ExplorerRangeParams {
  period?: ExplorerPeriod;
  date_from?: string;
  date_to?: string;
}

export interface UsageExplorerRow {
  id: string;
  name: string;
  email?: string;
  key_prefix?: string;
  subscribers?: number;
  requests: number;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  total_cost: number;
  avg_latency_ms: number;
}

export interface UsageExplorerSummary {
  requests: number;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  total_cost: number;
}

export interface UsageExplorerAggregate {
  dimension: ExplorerDimension;
  range: string;
  start: string;
  end: string;
  limit: number;
  rows: UsageExplorerRow[];
  summary: UsageExplorerSummary;
}

export interface UsageExplorerLogs {
  dimension: ExplorerDimension;
  value: string;
  range: string;
  records: ExplorerUsageRecord[];
  total: number;
  page: number;
  limit: number;
  total_pages: number;
}

/** Raw record shape of internal/query.UsageRecord — note `cost` is micro-USD
 *  (int64), unlike the USD float exposed by /api/billing/usage-records. */
export interface ExplorerUsageRecord {
  trace_id: string;
  model: string;
  provider: string;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  requests: number;
  cost: number;
  latency_ms: number;
  created_at: string;
  endpoint: string;
  error_code?: string;
  is_streaming?: boolean;
  status: string;
  group_id?: string;
  group_name?: string;
  api_key_name?: string;
  key_prefix?: string;
}

function rangeQuery(r: ExplorerRangeParams): string {
  const q = new URLSearchParams();
  if (r.date_from && r.date_to) {
    q.set('date_from', r.date_from);
    q.set('date_to', r.date_to);
  } else {
    q.set('period', r.period ?? '7d');
  }
  return q.toString();
}

export async function getUsageExplorerAggregate(
  dimension: ExplorerDimension,
  range: ExplorerRangeParams,
  limit = 50
): Promise<UsageExplorerAggregate> {
  const q = new URLSearchParams({ dimension, limit: String(limit) });
  for (const [k, v] of new URLSearchParams(rangeQuery(range))) q.set(k, v);
  return request<UsageExplorerAggregate>(`/api/admin/usage-explorer/aggregate?${q.toString()}`);
}

export async function getUsageExplorerLogs(
  dimension: ExplorerDimension,
  value: string,
  range: ExplorerRangeParams,
  page = 1,
  limit = 20
): Promise<UsageExplorerLogs> {
  const q = new URLSearchParams({
    dimension,
    value,
    page: String(page),
    limit: String(limit),
  });
  for (const [k, v] of new URLSearchParams(rangeQuery(range))) q.set(k, v);
  return request<UsageExplorerLogs>(`/api/admin/usage-explorer/logs?${q.toString()}`);
}

export async function exportUsageExplorerAggregate(
  dimension: ExplorerDimension,
  range: ExplorerRangeParams
): Promise<void> {
  await download(
    `/api/admin/usage-explorer/export/aggregate?dimension=${dimension}&${rangeQuery(range)}`
  );
}

export async function exportUsageExplorerLogs(
  dimension: ExplorerDimension,
  value: string,
  range: ExplorerRangeParams
): Promise<void> {
  await download(
    `/api/admin/usage-explorer/export/logs?dimension=${dimension}&value=${encodeURIComponent(value)}&${rangeQuery(range)}`
  );
}
