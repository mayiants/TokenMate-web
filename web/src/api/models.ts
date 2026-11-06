import { request } from './client';

/**
 * Mirrors the live /api/models/public contract of ai-portal-go
 * (verified 2026-11-06 against http://127.0.0.1:3001).
 */
export interface PublicModel {
  /** downstream model id, e.g. "glm-5.3" */
  api_model_name: string;
  provider: string;
  manufacturer?: string | null;
  /** text | image | video | audio | embedding ... */
  model_type?: string | null;
  supported_types?: string[] | null;
  billing_type?: string | null; // pay_as_you_go | per_request | ...
  status?: string | null;
  context_window?: string | number | null;
  max_output_tokens?: number | null;
  description?: string | null;
  features?: string[] | null;
  /** marketing tags; absent in current API responses */
  tags?: string[] | null;
  input_price?: number | null;
  output_price?: number | null;
  reasoning_price?: number | null;
  input_price_cache_hit?: number | null;
  input_price_cache_miss?: number | null;
  /** per-request billing price */
  fixed_price?: number | null;
  capabilities?: Record<string, unknown> | null;
  /**
   * Tolerant extension point: fields the live API does not send yet
   * (discount percent / original price / hot flag ...) are read via
   * helpers and simply render nothing when absent.
   */
  [key: string]: unknown;
}

export interface PublicModelsResponse {
  models: PublicModel[];
}

/** Loose view of unknown API responses (allowed宽容类型). */
export type UnknownRecord = Record<string, unknown>;

export async function listPublicModels(): Promise<PublicModel[]> {
  const res = await request<PublicModelsResponse>('/api/models/public');
  return res.models ?? [];
}

export const BASE_URL = 'https://api.woodgit.com/v1';
export const CHAT_COMPLETIONS_PATH = '/v1/chat/completions';
