import { request, setToken } from './client';

/**
 * Auth contract of ai-portal-go (internal/routes/auth.go).
 * Login/register return a JWT that the web app stores and sends as
 * `Authorization: Bearer` (the backend never sets a session cookie).
 */
export interface AuthUser {
  id: string;
  name: string;
  email: string;
  role?: string;
  balance?: number;
  created_at?: string;
}

export interface AuthResponse {
  token: string;
  user: AuthUser;
}

/** Second-factor challenge — the backend returns this instead of a token when
 *  the account (or its MFA policy) requires TOTP verification. */
export interface MfaChallenge {
  mfa_required: true;
  mfa_token: string;
  expires_in?: number;
}

export type LoginResult =
  | { status: 'ok'; user: AuthUser }
  | { status: 'mfa'; mfaToken: string; expiresIn?: number };

export interface MeResponse {
  user: AuthUser & { balance?: number; created_at?: string };
}

export async function login(email: string, password: string): Promise<LoginResult> {
  const res = await request<AuthResponse | MfaChallenge>('/api/auth/login', {
    method: 'POST',
    body: { email, password },
  });
  if ((res as MfaChallenge).mfa_required) {
    const challenge = res as MfaChallenge;
    return { status: 'mfa', mfaToken: challenge.mfa_token, expiresIn: challenge.expires_in };
  }
  const ok = res as AuthResponse;
  setToken(ok.token);
  return { status: 'ok', user: ok.user };
}

/** Complete an MFA challenge (POST /api/auth/login/mfa). */
export async function loginMfa(
  mfaToken: string,
  factor: { code?: string; recoveryCode?: string }
): Promise<AuthUser> {
  const res = await request<AuthResponse>('/api/auth/login/mfa', {
    method: 'POST',
    body: {
      mfa_token: mfaToken,
      code: factor.code ?? '',
      recovery_code: factor.recoveryCode ?? '',
    },
  });
  setToken(res.token);
  return res.user;
}

export async function register(payload: {
  name?: string;
  email: string;
  password: string;
  code: string;
}): Promise<AuthUser> {
  const res = await request<AuthResponse>('/api/auth/register', {
    method: 'POST',
    body: payload,
  });
  setToken(res.token);
  return res.user;
}

export async function sendCode(email: string): Promise<void> {
  await request<{ success: boolean }>('/api/auth/send-code', {
    method: 'POST',
    body: { email },
  });
}

export async function fetchMe(): Promise<AuthUser> {
  const res = await request<MeResponse>('/api/auth/me');
  return res.user;
}

export async function logout(): Promise<void> {
  try {
    await request<{ success: boolean }>('/api/auth/logout', { method: 'POST' });
  } finally {
    setToken(null);
  }
}

export async function forgotPassword(email: string): Promise<void> {
  await request('/api/auth/forgot-password', { method: 'POST', body: { email } });
}

export async function resetPassword(payload: {
  email: string;
  code: string;
  password: string;
}): Promise<void> {
  await request('/api/auth/reset-password', { method: 'POST', body: payload });
}
