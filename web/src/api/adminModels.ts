import { download, request } from './client';

// Admin model catalogue + pricing rules (internal/routes/admin_models.go,
// models_common.go, admin_pricing.go). All endpoints require system admin.

export interface ServiceModel {
  id: string;
  provider: string;
  product_id: string;
  api_model_name?: string | null;
  vendor?: string | null;
  /** active | pending | paused | ignored | eol ... */
  status: string;
  validation_status?: string | null;
  validated_at?: string | null;
  input_price?: number | null;
  output_price?: number | null;
  input_price_cache_hit?: number | null;
  input_price_cache_miss?: number | null;
  reasoning_price?: number | null;
  fixed_price?: number | null;
  billing_type?: string | null;
  context_window?: number;
  max_output_tokens?: number;
  /** text | image | video | audio | embedding */
  model_type?: string;
  hidden?: boolean;
  show_in_store?: boolean;
  capabilities?: Record<string, unknown>;
  created_at?: string;
}

export function listServiceModels(): Promise<ServiceModel[]> {
  return request<{ models?: ServiceModel[] }>('/api/admin/service-models').then(
    (res) => res.models ?? []
  );
}

/** status (active/paused/ignored...) drives the on/off-shelf switch. */
export async function updateServiceModel(
  id: string,
  payload: {
    status?: string;
    api_model_name?: string;
    input_price?: number;
    output_price?: number;
    context_window?: number;
    max_output_tokens?: number;
    show_in_store?: boolean;
  }
): Promise<void> {
  await request(`/api/admin/service-models/${id}`, { method: 'PATCH', body: payload });
}

/** Import models from the bundled aiModels.json catalogue. */
export async function importServiceModels(): Promise<{
  added?: number;
  skipped?: number;
  hasChanges?: boolean;
}> {
  return request('/api/admin/service-models/sync-from-aimodels', { method: 'POST' });
}

/** Force-push the active catalogue to every upstream key (heavy). */
export async function resyncModelsToKeys(): Promise<{ keys_updated?: number }> {
  return request('/api/admin/service-models/resync-to-keys', { method: 'POST' });
}

// --- pricing rules ----------------------------------------------------------

export interface ComponentPrices {
  input?: number;
  output?: number;
  cache_hit?: number;
  cache_write?: number;
  reasoning?: number;
  fixed_request?: number;
}

export interface PricingRule {
  id: string;
  provider: string;
  region?: string;
  product_id?: string;
  api_model_name?: string;
  billing_mode: string;
  prices?: ComponentPrices;
  tiers?: unknown[];
  time_rules?: unknown[];
  timezone?: string;
  currency?: string;
  priority?: number;
  status?: string;
  rule_name?: string;
  note?: string;
  block_seconds?: number;
  created_at?: string;
  updated_at?: string;
}

export interface PricingRulePayload {
  provider: string;
  billing_mode: string;
  region?: string;
  product_id?: string;
  api_model_name?: string;
  prices?: ComponentPrices;
  priority?: number;
  status?: string;
  rule_name?: string;
  note?: string;
  timezone?: string;
  currency?: string;
}

export function listPricingRules(): Promise<PricingRule[]> {
  return request<{ rules?: PricingRule[] }>('/api/admin/pricing').then((res) => res.rules ?? []);
}

export async function createPricingRule(payload: PricingRulePayload): Promise<string> {
  const res = await request<{ id?: string }>('/api/admin/pricing', {
    method: 'POST',
    body: payload,
  });
  return res.id ?? '';
}

/** PATCH requires the full rule body (provider + billing_mode are required). */
export async function updatePricingRule(id: string, payload: PricingRulePayload): Promise<void> {
  await request(`/api/admin/pricing/${id}`, { method: 'PATCH', body: payload });
}

export async function togglePricingRule(id: string): Promise<void> {
  await request(`/api/admin/pricing/${id}/toggle`, { method: 'POST' });
}

export async function deletePricingRule(id: string): Promise<void> {
  await request(`/api/admin/pricing/${id}`, { method: 'DELETE' });
}

export function exportPricingCsv(): Promise<void> {
  return download('/api/admin/pricing/export/csv');
}
