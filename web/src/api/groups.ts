import { request } from './client';

/**
 * User-visible groups of ai-portal-go (internal/routes/groups.go, RegisterPublic).
 * GET /api/groups returns the groups the caller may bind a key to, already
 * filtered by role: platform groups (general/economy), inherited enterprise
 * templates and the caller's own enterprise public groups.
 *
 * Rate limits and the model whitelist live on the group and are inherited by
 * every key that binds to it — this is why the key form cannot invent its own
 * group list client-side.
 */
export interface Group {
  id: string;
  name: string;
  description?: string | null;
  /** general | economy | subscription | enterprise */
  group_type: string;
  /** token | request */
  billing_mode?: string;
  pricing_ratio?: number;
  /** model whitelist; empty/null means "all models" */
  models?: string[] | string | null;
  rate_limit_rpm?: number | null;
  /** not returned by GET /api/groups today — optional so the UI degrades quietly */
  rate_limit_tpm?: number | null;
  concurrent_limit?: number;
  status?: string;
  /** platform template inherited by an enterprise */
  inherited?: boolean;
}

/** pgx may hand jsonb back as an array or as a JSON string; normalize both. */
export function groupModels(group: Group | null | undefined): string[] {
  const raw = group?.models;
  if (!raw) return [];
  if (Array.isArray(raw)) return raw.filter((m): m is string => typeof m === 'string' && m.length > 0);
  try {
    const parsed: unknown = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed.filter((m): m is string => typeof m === 'string' && m.length > 0) : [];
  } catch {
    return [];
  }
}

/** group_types a key of the given type may bind to (mirrors createKey's checks). */
export function compatibleGroupTypes(keyType: string): string[] {
  if (keyType === 'enterprise') return ['enterprise'];
  return ['general', 'economy'];
}

export async function listMyGroups(): Promise<Group[]> {
  const res = await request<{ groups: Group[] }>('/api/groups');
  return res.groups ?? [];
}
