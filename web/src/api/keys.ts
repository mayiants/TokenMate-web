import { request } from './client';

/**
 * API key contract of ai-portal-go (internal/routes/apikeys_crud.go).
 * Both /api/keys and /api/apikeys are mounted; we use /api/keys.
 * The full key is returned ONLY at creation (and via audited ?reveal=true).
 */
export interface ApiKey {
  id: string;
  /** masked, e.g. "sk-wgit-abc1..." (first 12 chars + "...") */
  key_prefix: string;
  name: string;
  /** active | disabled | revoked | inactive */
  status: string;
  access_mode?: string;
  allowed_models?: string[] | null;
  rate_limit_rpm?: number;
  rate_limit_tpm?: number;
  total_requests?: number;
  total_tokens?: number;
  group_id?: string;
  group_name?: string;
  key_type?: string;
  billing_mode?: string;
  current_model?: string;
  model_routing_mode?: string;
  selected_models?: string[] | null;
  created_at: string;
  expires_at?: string | null;
}

export async function listKeys(): Promise<ApiKey[]> {
  const res = await request<{ keys: ApiKey[] }>('/api/keys');
  return res.keys ?? [];
}

/** Server whitelist: createKey rejects anything else (enterprise_keys.go). */
export type KeyExpiresIn = 0 | 30 | 90 | 365;

/** general | subscription | enterprise — validated by createKey. */
export type KeyType = 'general' | 'subscription' | 'enterprise';

/** single | round_robin | priority. */
export type KeyRoutingMode = 'single' | 'round_robin' | 'priority';

/**
 * Mirrors the createKey body of ai-portal-go. Domain coupling enforced server-side:
 * - non-subscription keys must carry `group_priority` (first item becomes group_id)
 * - subscription keys must carry `subscription_id`; the plan pool replaces the group pool
 * - round_robin / priority require a non-empty `selected_models`
 * - `selected_models` must be a subset of the group's (or plan's) model list
 * - rpm/tpm of 0 mean "inherit the group limit"
 */
export interface CreateKeyPayload {
  name: string;
  key_type: KeyType;
  expires_in: KeyExpiresIn;
  model_routing_mode?: KeyRoutingMode;
  current_model?: string;
  selected_models?: string[];
  group_priority?: string[];
  subscription_id?: string;
  rate_limit_rpm?: number;
  rate_limit_tpm?: number;
  allowed_models?: string[];
}

export interface CreatedKey extends ApiKey {
  /** full plaintext key, present only in the creation response */
  key?: string;
}

export async function createKey(payload: CreateKeyPayload): Promise<CreatedKey> {
  return request<CreatedKey>('/api/keys', { method: 'POST', body: payload });
}

export async function getKey(id: string, reveal = false): Promise<ApiKey & { key?: string }> {
  const res = await request<{ key: ApiKey & { key?: string } }>(`/api/keys/${id}${reveal ? '?reveal=true' : ''}`);
  return res.key;
}

export interface UpdateKeyPayload {
  name?: string;
  /** only "active" | "inactive" allowed for users */
  status?: 'active' | 'inactive';
  selected_models?: string[];
}

export async function updateKey(id: string, payload: UpdateKeyPayload): Promise<void> {
  await request<{ success: boolean }>(`/api/keys/${id}`, { method: 'PATCH', body: payload });
}

export async function deleteKey(id: string): Promise<void> {
  await request<{ success: boolean }>(`/api/keys/${id}`, { method: 'DELETE' });
}

/** "2026-09-06T13:53:58Z" -> "2026-09-06 13:53:58"; null -> never-expires handled by caller. */
export function maskKeyDisplay(prefix: string): string {
  if (!prefix) return '—';
  return prefix.endsWith('...') ? `${prefix.slice(0, -3)}************` : prefix;
}
