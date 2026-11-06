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
