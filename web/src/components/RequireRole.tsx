import { useEffect, useState, type ReactNode } from 'react';
import { Navigate, useLocation } from 'react-router-dom';
import { Spin } from '@douyinfe/semi-ui';
import { useAuth } from '../auth/AuthContext';
import { useI18n } from '../i18n';
import { getMyEnterprise } from '../api/enterprise';

function Loading() {
  const { t } = useI18n();
  return (
    <div className="wg-center" style={{ minHeight: '60vh' }} aria-label={t('common.loading')}>
      <Spin size="large" />
    </div>
  );
}

/**
 * Route guard for /console/admin/*: only platform admins (system_admin /
 * super_admin) may open these pages. The backend 403s every /api/admin/*
 * endpoint for anyone else; this guard keeps the pages themselves out of
 * reach and matches the sider visibility rule.
 */
export function RequireAdminRole({ children }: { children: ReactNode }) {
  const { user, loading } = useAuth();
  if (loading) return <Loading />;
  if (!user) return <Navigate to="/login" replace />;
  if (user.role !== 'system_admin' && user.role !== 'super_admin') {
    return <Navigate to="/console" replace />;
  }
  return <>{children}</>;
}

/**
 * Route guard for /console/enterprise/*: only actual enterprise members
 * (backend /api/enterprise answers 200) may open these pages. Mirrors the
 * sider visibility probe in ConsoleLayout.
 */
export function RequireEnterpriseMember({ children }: { children: ReactNode }) {
  const { user, loading } = useAuth();
  const location = useLocation();
  const [member, setMember] = useState<boolean | null>(null);

  useEffect(() => {
    let cancelled = false;
    getMyEnterprise()
      .then(() => !cancelled && setMember(true))
      .catch(() => !cancelled && setMember(false));
    return () => {
      cancelled = true;
    };
  }, []);

  if (loading || member === null) return <Loading />;
  if (!user) return <Navigate to="/login" state={{ from: location.pathname }} replace />;
  if (!member) return <Navigate to="/console" replace />;
  return <>{children}</>;
}
