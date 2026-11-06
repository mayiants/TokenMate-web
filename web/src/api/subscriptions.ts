import { request } from './client';

/**
 * Caller's subscriptions (internal/routes/subscriptions_subscriptions.go,
 * getMySubscriptions → GET /api/subscriptions/my).
 *
 * A `subscription` key binds to one of these rows; the plan's model_permissions
 * becomes the key's model pool (createKey reads it server-side), so the UI must
 * never invent a model list for subscription keys.
 */
export interface UserSubscription {
  id: string;
  plan_id?: string | null;
  plan_name?: string;
  plan_type?: string;
  /** model pool of the plan; null/empty = unrestricted */
  model_permissions?: string[] | string | null;
  quota_total?: number;
  quota_remaining?: number;
  status?: string;
  started_at?: string | null;
  expires_at?: string | null;
  can_renew?: boolean;
}

export function planModels(sub: UserSubscription | null | undefined): string[] {
  const raw = sub?.model_permissions;
  if (!raw) return [];
  if (Array.isArray(raw)) return raw.filter((m): m is string => typeof m === 'string' && m.length > 0);
  try {
    const parsed: unknown = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed.filter((m): m is string => typeof m === 'string' && m.length > 0) : [];
  } catch {
    return [];
  }
}

export function isActiveSubscription(sub: UserSubscription): boolean {
  if (sub.status !== 'active') return false;
  if (!sub.expires_at) return true;
  const end = new Date(sub.expires_at).getTime();
  return Number.isNaN(end) || end > Date.now();
}

export async function listMySubscriptions(): Promise<UserSubscription[]> {
  const res = await request<{ subscriptions: UserSubscription[] }>('/api/subscriptions/my');
  return (res.subscriptions ?? []).filter(isActiveSubscription);
}
