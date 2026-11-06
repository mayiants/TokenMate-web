import { useEffect, useState } from 'react';
import { Link, Outlet, useNavigate } from 'react-router-dom';
import { Avatar, Button, Tooltip } from '@douyinfe/semi-ui';
import {
  IconChevronRight,
  IconClose,
  IconGift,
  IconLanguage,
  IconMoon,
  IconSun,
  IconBolt,
} from '@douyinfe/semi-icons';
import { useI18n } from '../i18n';
import { useTheme } from '../utils/theme';
import { useAuth } from '../auth/AuthContext';

const BANNER_STORAGE_KEY = 'wg-promo-closed-v1';

export default function PublicLayout() {
  const { locale, setLocale, t } = useI18n();
  const { mode, toggle } = useTheme();
  const { user } = useAuth();
  const navigate = useNavigate();
  const [bannerClosed, setBannerClosed] = useState(true);

  useEffect(() => {
    setBannerClosed(localStorage.getItem(BANNER_STORAGE_KEY) === '1');
  }, []);

  const closeBanner = () => {
    localStorage.setItem(BANNER_STORAGE_KEY, '1');
    setBannerClosed(true);
  };

  return (
    <div className="wg-public">
      <header className="wg-public-header">
        <div className="wg-container wg-topnav">
          <Link to="/" className="wg-logo" aria-label="WOODGIT">
            <span className="wg-logo-dot">W</span>
            WOODGIT
          </Link>
          <nav className="wg-topnav-links" aria-label="main">
            <Link to="/models">{t('nav.models')}</Link>
            <a href="#enterprise" onClick={(e) => e.preventDefault()}>{t('nav.enterprise')}</a>
            <a href="#docs" onClick={(e) => e.preventDefault()}>{t('nav.docs')}</a>
          </nav>
          <div className="wg-topnav-actions">
            <Tooltip content={mode === 'dark' ? t('theme.toLight') : t('theme.toDark')}>
              <Button
                icon={mode === 'dark' ? <IconSun /> : <IconMoon />}
                theme="borderless"
                onClick={toggle}
                aria-label={t('theme.toggle')}
              />
            </Tooltip>
            <Button
              icon={<IconLanguage />}
              theme="borderless"
              onClick={() => setLocale(locale === 'zh' ? 'en' : 'zh')}
            >
              {locale === 'zh' ? 'EN' : '中文'}
            </Button>
            {user ? (
              <Tooltip content={t('nav.console')}>
                <button
                  type="button"
                  className="wg-user-chip wg-user-chip-nav"
                  onClick={() => navigate('/console')}
                >
                  <Avatar color="blue" size="small">
                    {(user.name || user.email || 'W').trim().charAt(0).toUpperCase()}
                  </Avatar>
                  <span className="wg-user-chip-name">{user.name || user.email}</span>
                </button>
              </Tooltip>
            ) : (
              <Button theme="light" className="wg-nav-signin" onClick={() => navigate('/login')}>
                {t('nav.login')}
              </Button>
            )}
            <Button theme="solid" type="primary">{t('nav.getKey')}</Button>
          </div>
        </div>
        {!bannerClosed && (
          <div className="wg-promo" role="region" aria-label={t('banner.label')}>
            <span className="wg-promo-seg">
              <span className="wg-promo-icon" aria-hidden>
                <IconBolt size="small" />
              </span>
              <span>{t('banner.seg1')}</span>
              <a className="wg-promo-link" href="#models" onClick={(e) => e.preventDefault()}>
                {t('banner.tryNow')}
                <IconChevronRight size="small" />
              </a>
            </span>
            <span className="wg-promo-seg">
              <span className="wg-promo-icon" aria-hidden>
                <IconGift size="small" />
              </span>
              <span>{t('banner.seg2')}</span>
              <a className="wg-promo-link" href="#topup" onClick={(e) => e.preventDefault()}>
                {t('banner.claimNow')}
                <IconChevronRight size="small" />
              </a>
            </span>
            <button type="button" className="wg-promo-close" onClick={closeBanner} aria-label={t('banner.close')}>
              <IconClose size="small" />
            </button>
          </div>
        )}
      </header>
      <main>
        <Outlet />
      </main>
    </div>
  );
}
