import { useEffect, useState } from 'react';
import { Outlet, useNavigate } from 'react-router-dom';
import { Button, Empty, Spin } from '@douyinfe/semi-ui';
import { IconShieldStroked } from '@douyinfe/semi-icons';
import { useI18n } from '../../i18n';
import { getAdminMode } from '../../api/admin';

const ADMIN_ROLES = ['system_admin', 'super_admin'];

type Probe = 'loading' | 'ok' | 'denied';

/**
 * Gate for /console/admin/*. The backend is the authority: every admin route
 * returns 403 for enterprise_admins and regular users, and the probe below
 * mirrors that decision in the UI so non-admins never see the shell.
 */
export default function AdminLayout() {
  const { t } = useI18n();
  const navigate = useNavigate();
  const [probe, setProbe] = useState<Probe>('loading');

  useEffect(() => {
    let cancelled = false;
    getAdminMode()
      .then((mode) => {
        if (cancelled) return;
        setProbe(ADMIN_ROLES.includes(mode.userRole ?? '') ? 'ok' : 'denied');
      })
      .catch(() => {
        if (!cancelled) setProbe('denied');
      });
    return () => {
      cancelled = true;
    };
  }, []);

  if (probe === 'loading') {
    return (
      <div className="wg-page-loading" aria-label={t('common.loading')}>
        <Spin size="large" />
      </div>
    );
  }

  if (probe === 'denied') {
    return (
      <div className="wg-page-loading">
        <Empty
          image={<IconShieldStroked size="extra-large" />}
          title={t('admin.denied.title')}
          description={t('admin.denied.desc')}
        >
          <Button type="primary" theme="solid" onClick={() => navigate('/console')}>
            {t('admin.denied.back')}
          </Button>
        </Empty>
      </div>
    );
  }

  return <Outlet />;
}
