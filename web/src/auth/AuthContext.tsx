import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';
import {
  fetchMe,
  login as apiLogin,
  loginMfa as apiLoginMfa,
  logout as apiLogout,
  register as apiRegister,
  type AuthUser,
  type LoginResult,
} from '../api/auth';
import { getToken, setToken } from '../api/client';
import { clearAdminModeCache } from '../api/admin';

interface AuthState {
  user: AuthUser | null;
  /** True while the initial session probe (GET /api/auth/me) is in flight. */
  loading: boolean;
  /** Returns `mfa` when a second factor is required; the caller then calls loginMfa. */
  login: (email: string, password: string) => Promise<LoginResult>;
  loginMfa: (mfaToken: string, factor: { code?: string; recoveryCode?: string }) => Promise<AuthUser>;
  register: (payload: { name?: string; email: string; password: string; code: string }) => Promise<AuthUser>;
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthState | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<AuthUser | null>(null);
  const [loading, setLoading] = useState(() => !!getToken());

  useEffect(() => {
    let cancelled = false;
    if (!getToken()) {
      setLoading(false);
      return;
    }
    fetchMe()
      .then((me) => {
        if (!cancelled) setUser(me);
      })
      .catch(() => {
        if (!cancelled) clearAdminModeCache();
    setToken(null);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const login = useCallback(async (email: string, password: string) => {
    clearAdminModeCache();
    const res = await apiLogin(email, password);
    if (res.status === 'ok') setUser(res.user);
    return res;
  }, []);

  const loginMfa = useCallback(async (mfaToken: string, factor: { code?: string; recoveryCode?: string }) => {
    clearAdminModeCache();
    const u = await apiLoginMfa(mfaToken, factor);
    setUser(u);
    return u;
  }, []);

  const register = useCallback(async (payload: { name?: string; email: string; password: string; code: string }) => {
    clearAdminModeCache();
    const u = await apiRegister(payload);
    setUser(u);
    return u;
  }, []);

  const logout = useCallback(async () => {
    await apiLogout();
    clearAdminModeCache();
    setUser(null);
  }, []);

  const value = useMemo(
    () => ({ user, loading, login, loginMfa, register, logout }),
    [user, loading, login, loginMfa, register, logout]
  );
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth() {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error('useAuth must be used within AuthProvider');
  return ctx;
}
