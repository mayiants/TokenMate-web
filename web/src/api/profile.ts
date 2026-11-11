import { request } from './client';
/**
 * Profile contract of ai-portal-go (internal/routes/profile.go).
 * Mounted at /api/profile (GET/PATCH, PATCH /password).
 * changePassword is the same handler as POST /api/auth/change-password:
 * it bumps token_version, so the current JWT is invalidated server-side.
 */
export interface ProfileUser {
  id: string;
  name: string;
  email: string;
  role: string;
  balance?: number;
  timezone?: string;
  phone?: string;
  company?: string;
  second_email?: string;
  created_at?: string;
}

export async function getProfile(): Promise<ProfileUser> {
  const res = await request<{ user: ProfileUser }>('/api/profile');
  return res.user;
}

export async function updateProfile(payload: {
  name?: string;
  company?: string;
  timezone?: string;
}): Promise<void> {
  await request<{ success: boolean }>('/api/profile', { method: 'PATCH', body: payload });
}

export async function changePassword(oldPassword: string, newPassword: string): Promise<void> {
  await request<{ success: boolean }>('/api/profile/password', {
    method: 'PATCH',
    body: { old_password: oldPassword, new_password: newPassword },
  });
}

// Preferred display currency — same contract as the legacy portal
// (GET/PUT /api/profile/currency).
export async function getCurrency(): Promise<string> {
  const res = await request<{ currency: string }>('/api/profile/currency');
  return res.currency;
}

export async function updateCurrency(currency: string): Promise<void> {
  await request<{ success: boolean }>('/api/profile/currency', {
    method: 'PUT',
    body: { currency },
  });
}

// Personal-data export / erasure — GDPR routes (mounted at /api/gdpr).
export interface GdprStatus {
  gdpr_data_exported_at?: string;
  gdpr_erasure_requested_at?: string;
}

export async function getGdprStatus(): Promise<GdprStatus> {
  return request<GdprStatus>('/api/gdpr/status');
}

export async function exportMyData(): Promise<unknown> {
  return request<unknown>('/api/gdpr/export');
}

export async function requestDataDeletion(): Promise<{ message?: string }> {
  return request<{ message?: string }>('/api/gdpr/delete', {
    method: 'POST',
    body: { confirm: 'DELETE_MY_DATA' },
  });
}

// Contact changes — verification-code flows (internal/routes/profile.go).
// Phone codes are not deliverable yet (no SMS gateway): the backend rejects
// send-change-code with type=phone.
export async function sendChangeCode(type: 'email' | 'phone', value: string): Promise<void> {
  await request<{ success: boolean }>('/api/profile/send-change-code', {
    method: 'POST',
    body: { type, value },
  });
}

export async function changeEmail(payload: {
  new_email: string;
  verification_code: string;
  password: string;
}): Promise<void> {
  await request<{ success: boolean }>('/api/profile/change-email', {
    method: 'PATCH',
    body: payload,
  });
}

export async function changePhone(payload: {
  new_phone: string;
  verification_code: string;
  password: string;
}): Promise<void> {
  await request<{ success: boolean }>('/api/profile/change-phone', {
    method: 'PATCH',
    body: payload,
  });
}

export async function sendBackupEmailCode(email: string): Promise<void> {
  await request<{ success: boolean }>('/api/profile/send-backup-email-code', {
    method: 'POST',
    body: { email },
  });
}

export async function changeBackupEmail(payload: {
  new_email: string;
  verification_code: string;
  password: string;
}): Promise<void> {
  await request<{ success: boolean }>('/api/profile/change-backup-email', {
    method: 'PATCH',
    body: payload,
  });
}

// ---------------------------------------------------------------------------
// Two-factor authentication (TOTP) — internal/routes/mfa.go, mounted at
// /api/profile/mfa. Enabling/disabling bumps token_version, so the backend
// returns a freshly issued token that must replace the stored one.
// ---------------------------------------------------------------------------
export interface MfaStatus {
  enabled: boolean;
  enabled_at?: string | null;
  setup_pending: boolean;
  recovery_codes_remaining: number;
  required: boolean;
}

export interface MfaSetup {
  secret: string;
  otpauth_uri: string;
  issuer: string;
  algorithm: string;
  digits: number;
  period: number;
}

export async function getMfaStatus(): Promise<MfaStatus> {
  return request<MfaStatus>('/api/profile/mfa/status');
}

export async function beginMfaSetup(): Promise<MfaSetup> {
  return request<MfaSetup>('/api/profile/mfa/setup', { method: 'POST' });
}

/** Confirms the authenticator works; returns the one-time recovery codes + token. */
export async function enableMfa(code: string): Promise<{ recovery_codes: string[]; token?: string }> {
  return request<{ success: boolean; recovery_codes: string[]; token?: string }>(
    '/api/profile/mfa/enable',
    { method: 'POST', body: { code } }
  );
}

export async function disableMfa(payload: {
  password: string;
  code?: string;
  recovery_code?: string;
}): Promise<{ token?: string }> {
  return request<{ success: boolean; token?: string }>('/api/profile/mfa/disable', {
    method: 'POST',
    body: payload,
  });
}

export async function regenerateRecoveryCodes(
  password: string,
  code: string
): Promise<{ recovery_codes: string[] }> {
  return request<{ success: boolean; recovery_codes: string[] }>(
    '/api/profile/mfa/recovery-codes/regenerate',
    { method: 'POST', body: { password, code } }
  );
}
