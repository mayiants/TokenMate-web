import { Link } from 'react-router-dom';
import { IconChevronLeft } from '@douyinfe/semi-icons';
import type { ReactNode } from 'react';
import { useI18n } from '../../i18n';

/** Centered card shell shared by /login and /register: brand area + form. */
export default function AuthCard({ title, subtitle, children }: { title: string; subtitle: string; children: ReactNode }) {
  const { t } = useI18n();
  return (
    <div className="wg-auth-page">
      <div className="wg-auth-card">
        <div className="wg-auth-brand">
          <Link to="/" className="wg-logo" aria-label="WOODGIT">
            <span className="wg-logo-dot">W</span>
            WOODGIT
          </Link>
          <h1 className="wg-auth-title">{title}</h1>
          <p className="wg-auth-subtitle">{subtitle}</p>
        </div>
        {children}
        <Link to="/" className="wg-auth-back">
          <IconChevronLeft size="small" />
          {t('auth.backHome')}
        </Link>
      </div>
    </div>
  );
}
