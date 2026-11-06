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

/** Full history including expired/cancelled rows (P5 "我的订阅" tab). */
export async function listAllMySubscriptions(): Promise<UserSubscription[]> {
  const res = await request<{ subscriptions: UserSubscription[] }>('/api/subscriptions/my');
  return res.subscriptions ?? [];
}

/**
 * Public purchasable plans (internal/routes/subscriptions_plans.go,
 * listPublicPlans → GET /api/subscriptions/plans). No auth required.
 * price / quota are raw micro-USD; duration_display is server-rendered.
 */
export interface SubscriptionPlan {
  id: string;
  name: string;
  description?: string | null;
  /** micro-USD */
  price: number;
  /** micro-USD quota */
  quota: number;
  duration_days?: number;
  duration_display?: string;
  daily_limit?: number;
  plan_type?: string;
  rpm_limit?: number;
  tpm_limit?: number;
  model_permissions?: string[] | string | null;
}

export function planModelNames(plan: SubscriptionPlan): string[] {
  const raw = plan.model_permissions;
  if (!raw) return [];
  if (Array.isArray(raw)) return raw.filter((m): m is string => typeof m === 'string' && m.length > 0);
  try {
    const parsed: unknown = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed.filter((m): m is string => typeof m === 'string' && m.length > 0) : [];
  } catch {
    return [];
  }
}

export async function listPublicPlans(): Promise<SubscriptionPlan[]> {
  const res = await request<{ plans: SubscriptionPlan[] }>('/api/subscriptions/plans');
  return res.plans ?? [];
}

export interface PurchaseResult {
  success: boolean;
  order_id?: string;
  order_uuid?: string;
  plan_name?: string;
  price?: number;
}

/** Creates a pending subscription order; activation happens after payment. */
export async function purchasePlan(planId: string): Promise<PurchaseResult> {
  return request<PurchaseResult>('/api/subscriptions/purchase', {
    method: 'POST',
    body: { plan_id: planId },
  });
}
