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
