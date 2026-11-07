import { request } from './client';

/**
 * Platform settings (P3-A3), sourced from internal/routes/settings.go:
 *   GET/PUT  /api/admin/settings
 *   PUT      /api/admin/settings/:key        (whitelisted keys only)
 *   POST     /api/admin/settings/reset
 *   GET/PUT/DELETE/POST /api/admin/settings/smtp-config[/test]
 *
 * The generic settings response is grouped; each entry carries `{value, updatedAt}`
 * and `defaults` holds the seed values used by "reset to default".
 */

export interface AdminSettingEntry {
  value: string;
  updatedAt?: string | null;
}

export interface AdminSettingGroup {
  name: string;
  settings: Record<string, AdminSettingEntry>;
}

export interface AdminSettingsResponse {
  settings?: Record<string, AdminSettingGroup>;
  groups?: Record<string, { Name: string; Keys: string[] }>;
  defaults?: Record<string, string>;
}

export function getAdminSettings(): Promise<AdminSettingsResponse> {
  return request<AdminSettingsResponse>('/api/admin/settings');
}

/** The backend validates against its allowlist; values are always strings. */
export async function updateAdminSetting(key: string, value: string): Promise<void> {
  await request(`/api/admin/settings/${encodeURIComponent(key)}`, {
    method: 'PUT',
    body: { value },
  });
}

/** Empty body resets every known key back to its seed default. */
export async function resetAdminSettings(keys?: string[]): Promise<string[]> {
  const res = await request<{ reset?: string[] }>('/api/admin/settings/reset', {
    method: 'POST',
    body: keys ? { keys } : {},
  });
  return res.reset ?? [];
}

export interface SmtpConfig {
  source?: 'db' | 'env' | string;
  has_password?: boolean;
  config?: {
    host?: string;
    port?: number;
    secure?: boolean;
    user?: string;
    from?: string;
  };
}

export function getSmtpConfig(): Promise<SmtpConfig> {
  return request<SmtpConfig>('/api/admin/settings/smtp-config');
}

export async function testSmtpConfig(to: string): Promise<void> {
  await request('/api/admin/settings/smtp-config/test', {
    method: 'POST',
    body: { to },
  });
}

/** Drops DB overrides and reloads the environment SMTP configuration. */
export async function deleteSmtpConfig(): Promise<void> {
  await request('/api/admin/settings/smtp-config', { method: 'DELETE' });
}
