import { useEffect, useMemo, useState } from 'react';
import { Link, Outlet, useLocation, useNavigate } from 'react-router-dom';
import { Avatar, Button, Dropdown, Nav, Tooltip } from '@douyinfe/semi-ui';
import {
  IconBellStroked,
  IconCalendarClockStroked,
  IconChevronLeft,
  IconChevronRight,
  IconCreditCardStroked,
  IconExit,
  IconHomeStroked,
  IconKeyStroked,
  IconList,
  IconMoon,
  IconServerStroked,
  IconSun,
  IconUserCircle,
  IconBriefcaseStroked,
  IconHistogram,
  IconFolderStroked,
  IconSettingStroked,
  IconUserGroup,
  IconLanguage,
  IconMailStroked,
  IconHelpCircleStroked,
} from '@douyinfe/semi-icons';
import { useAuth } from '../auth/AuthContext';
import { useI18n } from '../i18n';
import { useTheme } from '../utils/theme';
import { getMyEnterprise } from '../api/enterprise';

const COLLAPSE_STORAGE_KEY = 'wg-console-sider';

const NAV_ITEMS = [
  { itemKey: 'dashboard', textKey: 'console.nav.dashboard', path: '/console', icon: <IconHomeStroked /> },
  { itemKey: 'keys', textKey: 'console.nav.keys', path: '/console/keys', icon: <IconKeyStroked /> },
  { itemKey: 'logs', textKey: 'console.nav.logs', path: '/console/logs', icon: <IconList /> },
  { itemKey: 'balance', textKey: 'console.nav.balance', path: '/console/balance', icon: <IconCreditCardStroked /> },
];

const ACC_ITEMS = [
  { itemKey: 'acc-profile', textKey: 'acc.nav.profile', path: '/console/profile', icon: <IconUserCircle /> },
  { itemKey: 'acc-notifications', textKey: 'acc.nav.notifications', path: '/console/notifications', icon: <IconBellStroked /> },
  { itemKey: 'acc-support', textKey: 'acc.nav.support', path: '/console/support', icon: <IconHelpCircleStroked /> },
  { itemKey: 'acc-billing', textKey: 'acc.nav.billing', path: '/console/billing', icon: <IconCreditCardStroked /> },
  { itemKey: 'acc-subscriptions', textKey: 'acc.nav.subscriptions', path: '/console/subscriptions', icon: <IconCalendarClockStroked /> },
  { itemKey: 'acc-groups', textKey: 'acc.nav.groups', path: '/console/groups', icon: <IconFolderStroked /> },
  { itemKey: 'acc-status', textKey: 'acc.nav.status', path: '/console/status', icon: <IconServerStroked /> },
];

const ENT_ITEMS = [
  { itemKey: 'ent-overview', textKey: 'ent.nav.overview', path: '/console/enterprise', icon: <IconBriefcaseStroked /> },
  { itemKey: 'ent-members', textKey: 'ent.nav.members', path: '/console/enterprise/members', icon: <IconUserGroup /> },
  { itemKey: 'ent-groups', textKey: 'ent.nav.groups', path: '/console/enterprise/groups', icon: <IconFolderStroked /> },
  { itemKey: 'ent-keys', textKey: 'ent.nav.keys', path: '/console/enterprise/keys', icon: <IconKeyStroked /> },
  { itemKey: 'ent-usage', textKey: 'ent.nav.usage', path: '/console/enterprise/usage', icon: <IconHistogram /> },
  { itemKey: 'ent-billing', textKey: 'ent.nav.billing', path: '/console/enterprise/billing', icon: <IconCreditCardStroked /> },
  { itemKey: 'ent-settings', textKey: 'ent.nav.settings', path: '/console/enterprise/settings', icon: <IconSettingStroked /> },
];

export default function ConsoleLayout() {
  const { t, locale, setLocale } = useI18n();
  const { user, logout } = useAuth();
  const { mode, toggle } = useTheme();
  const navigate = useNavigate();
  const location = useLocation();
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem(COLLAPSE_STORAGE_KEY) === '1');
  const [hasEnterprise, setHasEnterprise] = useState(false);

  // Enterprise nav group only renders for users that actually belong to an
  // enterprise (backend decides; non-members get 403 from /api/enterprise).
  useEffect(() => {
    let cancelled = false;
    getMyEnterprise()
      .then(() => !cancelled && setHasEnterprise(true))
      .catch(() => !cancelled && setHasEnterprise(false));
    return () => {
      cancelled = true;
    };
  }, []);

  const selectedKey = useMemo(() => {
    // exact match or child-path match; longest-path-first so '/console/groups'
    // never shadows a future nested route and vice versa.
    const match = [...ACC_ITEMS, ...ENT_ITEMS]
      .sort((a, b) => b.path.length - a.path.length)
      .find((i) => location.pathname === i.path || location.pathname.startsWith(i.path + '/'));
    if (match) return match.itemKey;
    if (location.pathname.startsWith('/console/keys')) return 'keys';
    if (location.pathname.startsWith('/console/logs')) return 'logs';
    if (location.pathname.startsWith('/console/balance')) return 'balance';
    return 'dashboard';
  }, [location.pathname]);


  // Flat nav: section headers are non-clickable label items — nested Nav.Sub
  // groups corrupted their state on collapse and on header clicks, so we
  // render children as top-level items.
  const navItems = useMemo(() => {
    const base = NAV_ITEMS.map((i) => ({ ...i, text: t(i.textKey) }));
    const accHeader = {
      itemKey: 'acc-header',
      text: t('acc.nav.group'),
      icon: <IconUserCircle />,
      disabled: false,
      style: { pointerEvents: 'none' as const, fontSize: 12, color: 'var(--wg-text-2)', marginTop: 8 },
    };
    const accItems = ACC_ITEMS.map((i) => ({ ...i, text: t(i.textKey) }));
    if (!hasEnterprise) return [...base, accHeader, ...accItems];
    return [
      ...base,
      accHeader,
      ...accItems,
      { itemKey: 'ent-header', text: t('ent.nav.group'), icon: <IconMailStroked />, disabled: false, style: { pointerEvents: 'none' as const, fontSize: 12, color: 'var(--wg-text-2)', marginTop: 8 } },
      ...ENT_ITEMS.map((i) => ({ ...i, text: t(i.textKey) })),
    ];
  }, [hasEnterprise, t]);

  const toggleCollapse = () => {
    setCollapsed((prev) => {
      localStorage.setItem(COLLAPSE_STORAGE_KEY, prev ? '0' : '1');
      return !prev;
    });
  };

  const onLogout = async () => {
    await logout();
    navigate('/login', { replace: true });
  };

  const displayName = user?.name || user?.email || '';
  const initial = displayName.trim().charAt(0).toUpperCase() || 'W';

  return (
    <div className={`wg-console${collapsed ? ' wg-console-collapsed' : ''}`}>
      <aside className="wg-console-sider">
        <div className="wg-console-sider-logo">
          <Nav.Item
            itemKey="home"
            text={
              <Link to="/" className="wg-logo" aria-label="WOODGIT">
                <span className="wg-logo-dot">W</span>
                {!collapsed && <span className="wg-logo-text">WOODGIT</span>}
              </Link>
            }
          />
        </div>
        <Nav
          className="wg-console-nav"
          isCollapsed={collapsed}
          selectedKeys={[selectedKey]}
          items={navItems}
          onSelect={(data) => {
            const all = hasEnterprise
              ? [...NAV_ITEMS, ...ACC_ITEMS, ...ENT_ITEMS]
              : [...NAV_ITEMS, ...ACC_ITEMS];
            const item = all.find((i) => i.itemKey === data.itemKey);
            if (item) navigate(item.path);
          }}
        />
        <div className="wg-console-sider-bottom">
          <span className="wg-build-tag" title="build tag">{__BUILD_TAG__}</span>
          <Tooltip content={t('common.language')} position="right">
            <Button
              icon={<IconLanguage />}
              size="small"
              theme="borderless"
              onClick={() => setLocale(locale === 'zh' ? 'en' : 'zh')}
              aria-label={t('common.language')}
            >
              {locale === 'zh' ? '中' : 'EN'}
            </Button>
          </Tooltip>
          <Tooltip content={mode === 'dark' ? t('theme.toLight') : t('theme.toDark')} position="right">
            <Button
              icon={mode === 'dark' ? <IconSun /> : <IconMoon />}
              theme="borderless"
              onClick={toggle}
              aria-label={t('theme.toggle')}
            />
          </Tooltip>
          <Tooltip content={collapsed ? t('console.expand') : t('console.collapse')} position="right">
            <Button
              className="wg-console-collapse-btn"
              icon={collapsed ? <IconChevronRight /> : <IconChevronLeft />}
              theme="borderless"
              onClick={toggleCollapse}
              aria-label={collapsed ? t('console.expand') : t('console.collapse')}
            />
          </Tooltip>
        </div>
      </aside>
      <div className="wg-console-body">
        <header className="wg-console-header">
          <Dropdown
            trigger="click"
            position="bottomRight"
            render={
              <Dropdown.Menu>
                <Dropdown.Item icon={<IconUserCircle />} onClick={() => navigate('/console/profile')}>
                  {t('console.profile')}
                </Dropdown.Item>
                <Dropdown.Divider />
                <Dropdown.Item icon={<IconExit />} type="danger" onClick={onLogout}>
                  {t('console.logout')}
                </Dropdown.Item>
              </Dropdown.Menu>
            }
          >
            <button type="button" className="wg-user-chip">
              <Avatar color="blue" size="small">
                {initial}
              </Avatar>
              <span className="wg-user-chip-name">{displayName}</span>
            </button>
          </Dropdown>
        </header>
        <main className="wg-console-content">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
