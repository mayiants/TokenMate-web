import { useEffect, useMemo, useState, type CSSProperties, type ReactNode } from 'react';
import { Link, Outlet, useLocation, useNavigate } from 'react-router-dom';
import { Avatar, Button, Dropdown, Nav, Tag, Tooltip } from '@douyinfe/semi-ui';
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
  IconShieldStroked,
  IconUserGroup,
  IconLanguage,
  IconMailStroked,
  IconHelpCircleStroked,
  IconComponent,
  IconCloudStroked,
  IconTicketCode,
  IconPulse,
} from '@douyinfe/semi-icons';
import { useAuth } from '../auth/AuthContext';
import { useI18n } from '../i18n';
import { useTheme } from '../utils/theme';
import { getMyEnterprise } from '../api/enterprise';
import { getAdminModeCached } from '../api/admin';

const COLLAPSE_STORAGE_KEY = 'wg-console-sider';

// One flattened sider entry: either a navigable page (path) or a section
// header (no path, pointerEvents none via style).
interface SiderItem {
  itemKey: string;
  text: string;
  path?: string;
  icon?: ReactNode;
  disabled?: boolean;
  style?: CSSProperties;
}

const NAV_ITEMS = [
  { itemKey: 'dashboard', textKey: 'console.nav.dashboard', path: '/console', icon: <IconHomeStroked /> },
  { itemKey: 'keys', textKey: 'console.nav.keys', path: '/console/keys', icon: <IconKeyStroked /> },
  { itemKey: 'logs', textKey: 'console.nav.logs', path: '/console/logs', icon: <IconList /> },
  { itemKey: 'balance', textKey: 'console.nav.balance', path: '/console/balance', icon: <IconCreditCardStroked /> },
  { itemKey: 'billing', textKey: 'acc.nav.billing', path: '/console/billing', icon: <IconCreditCardStroked /> },
  { itemKey: 'subscriptions', textKey: 'acc.nav.subscriptions', path: '/console/subscriptions', icon: <IconCalendarClockStroked /> },
  { itemKey: 'groups', textKey: 'acc.nav.groups', path: '/console/groups', icon: <IconFolderStroked /> },
  { itemKey: 'status', textKey: 'acc.nav.status', path: '/console/status', icon: <IconServerStroked /> },
];

// Account-level pages (profile / notifications / support) live in the
// top-right user dropdown, not in the sider.
const USER_MENU_ITEMS = [
  { itemKey: 'acc-profile', textKey: 'acc.nav.profile', path: '/console/profile', icon: <IconUserCircle /> },
  { itemKey: 'acc-notifications', textKey: 'acc.nav.notifications', path: '/console/notifications', icon: <IconBellStroked /> },
  { itemKey: 'acc-support', textKey: 'acc.nav.support', path: '/console/support', icon: <IconHelpCircleStroked /> },
];

const ENT_ITEMS = [
  { itemKey: 'ent-overview', textKey: 'ent.nav.overview', path: '/console/enterprise', icon: <IconBriefcaseStroked /> },
  { itemKey: 'ent-members', textKey: 'ent.nav.members', path: '/console/enterprise/members', icon: <IconUserGroup /> },
  { itemKey: 'ent-groups', textKey: 'ent.nav.groups', path: '/console/enterprise/groups', icon: <IconFolderStroked /> },
  { itemKey: 'ent-keys', textKey: 'ent.nav.keys', path: '/console/enterprise/keys', icon: <IconKeyStroked /> },
  { itemKey: 'ent-usage', textKey: 'ent.nav.usage', path: '/console/enterprise/usage', icon: <IconHistogram /> },
  { itemKey: 'ent-billing', textKey: 'ent.nav.billing', path: '/console/enterprise/billing', icon: <IconCreditCardStroked /> },
  { itemKey: 'ent-settings', textKey: 'ent.nav.settings', path: '/console/enterprise/settings', icon: <IconSettingStroked /> },
  { itemKey: 'ent-audit', textKey: 'ent.nav.audit', path: '/console/enterprise/audit', icon: <IconShieldStroked /> },
  { itemKey: 'ent-alerts', textKey: 'ent.nav.alerts', path: '/console/enterprise/alerts', icon: <IconBellStroked /> },
];

// Platform administration — only rendered for system_admin / super_admin
// (the backend 403s every /api/admin/* route for anyone else).
const ADMIN_ITEMS = [
  { itemKey: 'admin-overview', textKey: 'admin.nav.overview', path: '/console/admin', icon: <IconHistogram /> },
  { itemKey: 'admin-users', textKey: 'admin.nav.users', path: '/console/admin/users', icon: <IconUserGroup /> },
  { itemKey: 'admin-enterprises', textKey: 'admin.nav.enterprises', path: '/console/admin/enterprises', icon: <IconBriefcaseStroked /> },
  { itemKey: 'admin-groups', textKey: 'admin.nav.groups', path: '/console/admin/groups', icon: <IconFolderStroked /> },
  { itemKey: 'admin-models', textKey: 'admin.nav.models', path: '/console/admin/models', icon: <IconComponent /> },
  { itemKey: 'admin-upstream', textKey: 'admin.nav.upstream', path: '/console/admin/upstream', icon: <IconCloudStroked /> },
  { itemKey: 'admin-explorer', textKey: 'admin.nav.explorer', path: '/console/admin/usage-explorer', icon: <IconHistogram /> },
  { itemKey: 'admin-rate-limits', textKey: 'admin.nav.rateLimits', path: '/console/admin/rate-limits', icon: <IconPulse /> },
  { itemKey: 'admin-finance', textKey: 'admin.nav.finance', path: '/console/admin/finance', icon: <IconCreditCardStroked /> },
  { itemKey: 'admin-ops', textKey: 'admin.nav.ops', path: '/console/admin/ops', icon: <IconTicketCode /> },
  { itemKey: 'admin-security', textKey: 'admin.nav.security', path: '/console/admin/security', icon: <IconShieldStroked /> },
  { itemKey: 'admin-settings', textKey: 'admin.nav.settings', path: '/console/admin/settings', icon: <IconSettingStroked /> },
];

export default function ConsoleLayout() {
  const { t, locale, setLocale } = useI18n();
  const { user, logout } = useAuth();
  const { mode, toggle } = useTheme();
  const navigate = useNavigate();
  const location = useLocation();
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem(COLLAPSE_STORAGE_KEY) === '1');
  const [hasEnterprise, setHasEnterprise] = useState(false);
  const [hasAdmin, setHasAdmin] = useState(false);

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

  // Platform-admin visibility. The login/me role is authoritative — a stale
  // cached admin-mode probe must never resurrect the admin group after an
  // account switch (clearAdminModeCache on login/logout is the second layer).
  const isPlatformAdmin = user?.role === 'system_admin' || user?.role === 'super_admin';
  useEffect(() => {
    if (!isPlatformAdmin) {
      setHasAdmin(false);
      return;
    }
    let cancelled = false;
    getAdminModeCached()
      .then((mode) => {
        if (!cancelled) {
          setHasAdmin(mode.userRole === 'system_admin' || mode.userRole === 'super_admin');
        }
      })
      .catch(() => !cancelled && setHasAdmin(false));
    return () => {
      cancelled = true;
    };
  }, [isPlatformAdmin]);

  const selectedKey = useMemo(() => {
    // exact match or child-path match; longest-path-first so '/console/groups'
    // never shadows a future nested route and vice versa.
    const match = [...NAV_ITEMS, ...ENT_ITEMS, ...ADMIN_ITEMS]
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
  const navItems = useMemo<SiderItem[]>(() => {
    const base = NAV_ITEMS.map((i) => ({ ...i, text: t(i.textKey) }));
    const adminHeader: SiderItem = {
      itemKey: 'admin-header',
      text: t('admin.nav.group'),
      icon: <IconSettingStroked />,
      disabled: false,
      style: { pointerEvents: 'none' as const, fontSize: 12, color: 'var(--wg-text-2)', marginTop: 8 },
    };
    const adminItems = ADMIN_ITEMS.map((i) => ({ ...i, text: t(i.textKey) }));
    const items: SiderItem[] = [...base];
    if (hasEnterprise) {
      items.push(
        { itemKey: 'ent-header', text: t('ent.nav.group'), icon: <IconMailStroked />, disabled: false, style: { pointerEvents: 'none' as const, fontSize: 12, color: 'var(--wg-text-2)', marginTop: 8 } },
        ...ENT_ITEMS.map((i) => ({ ...i, text: t(i.textKey) }))
      );
    }
    if (hasAdmin) items.push(adminHeader, ...adminItems);
    return items;
  }, [hasEnterprise, hasAdmin, t]);

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
            const all = [
              ...NAV_ITEMS,
              ...(hasEnterprise ? ENT_ITEMS : []),
              ...(hasAdmin ? ADMIN_ITEMS : []),
            ];
            const item = all.find((i) => i.itemKey === data.itemKey);
            if (item) navigate(item.path);
          }}
        />
        <div className="wg-console-sider-bottom">
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
                <Dropdown.Item disabled>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, maxWidth: 220 }}>
                    <span
                      style={{
                        overflow: 'hidden',
                        textOverflow: 'ellipsis',
                        whiteSpace: 'nowrap',
                        fontWeight: 600,
                      }}
                    >
                      {displayName}
                    </span>
                    {user?.role && user.role !== 'user' && (
                      <Tag
                        size="small"
                        color={
                          user.role === 'super_admin'
                            ? 'red'
                            : user.role === 'system_admin'
                              ? 'violet'
                              : 'blue'
                        }
                      >
                        {(() => {
                          const key = `profile.role.${user.role}`;
                          const v = t(key);
                          return v === key ? user.role : v;
                        })()}
                      </Tag>
                    )}
                  </div>
                </Dropdown.Item>
                <Dropdown.Divider />
                {USER_MENU_ITEMS.map((i) => (
                  <Dropdown.Item key={i.itemKey} icon={i.icon} onClick={() => navigate(i.path)}>
                    {t(i.textKey)}
                  </Dropdown.Item>
                ))}
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
