import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react';
import { useLocation, useNavigate, Navigate } from 'react-router-dom';
import { Empty, Spin } from '@douyinfe/semi-ui';
import { useAuth } from '../../auth/AuthContext';
import type { AuthUser } from '../../api/auth';
import { getMyEnterprise, type EnterpriseInfo } from '../../api/enterprise';

interface EntContextValue {
  info: EnterpriseInfo;
  isAdmin: boolean;
  user: AuthUser | null;
  reload: () => Promise<void>;
}

const EntContext = createContext<EntContextValue | null>(null);

export function useEnterprise(): EntContextValue {
  const ctx = useContext(EntContext);
  if (!ctx) throw new Error('useEnterprise must be used under EntLayout');
  return ctx;
}

/** Loads the caller's enterprise and guards /console/enterprise/* routes. */
export default function EntLayout({ children }: { children: ReactNode }) {
  const { user } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const [info, setInfo] = useState<EnterpriseInfo | null | undefined>(undefined);

  const reload = useCallback(async () => {
    try {
      setInfo(await getMyEnterprise());
    } catch {
      setInfo(null);
    }
  }, []);

  useEffect(() => {
    reload();
  }, [reload]);

  if (info === undefined) {
    return (
      <div className="wg-page-loading">
        <Spin size="large" />
      </div>
    );
  }
  if (info === null) {
    return (
      <Empty
        title="Enterprise"
        description="You are not a member of any enterprise."
      >
        <a onClick={() => navigate('/console', { replace: true })}>Back to console</a>
      </Empty>
    );
  }

  // Members without the enterprise_admin role only get the overview; other
  // routes bounce back here (server enforces the same rule with 403s).
  const isAdmin = user?.role === 'enterprise_admin';
  if (!isAdmin && location.pathname !== '/console/enterprise') {
    return <Navigate to="/console/enterprise" replace />;
  }

  return <EntContext.Provider value={{ info, isAdmin, user, reload }}>{children}</EntContext.Provider>;
}
