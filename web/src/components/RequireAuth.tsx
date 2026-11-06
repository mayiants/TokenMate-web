import { Navigate, useLocation } from 'react-router-dom';
import { Spin } from '@douyinfe/semi-ui';
import type { ReactNode } from 'react';
import { useAuth } from '../auth/AuthContext';
import { useI18n } from '../i18n';

/** Route guard: redirects unauthenticated users to /login. */
export default function RequireAuth({ children }: { children: ReactNode }) {
  const { user, loading } = useAuth();
  const location = useLocation();
  const { t } = useI18n();

  if (loading) {
    return (
      <div className="wg-center" style={{ minHeight: '60vh' }} aria-label={t('common.loading')}>
        <Spin size="large" />
      </div>
    );
  }
  if (!user) {
    return <Navigate to="/login" state={{ from: location.pathname }} replace />;
  }
  return <>{children}</>;
}
