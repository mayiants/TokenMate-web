import { useCallback, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button, Input, Select, Spin, Tag, Toast } from '@douyinfe/semi-ui';
import {
  IconDeleteStroked,
  IconDownloadStroked,
  IconGlobeStroke,
  IconLockStroked,
  IconUserStroked,
} from '@douyinfe/semi-icons';
import PageHead from '../../components/PageHead';
import dangerConfirm from '../../components/dangerConfirm';
import ContactCard from './ContactCard';
import MfaCard from './MfaCard';
import { useI18n } from '../../i18n';
import { setToken } from '../../api/client';
import {
  changePassword,
  exportMyData,
  getCurrency,
  getGdprStatus,
  getProfile,
  requestDataDeletion,
  updateCurrency,
  updateProfile,
  type GdprStatus,
  type ProfileUser,
} from '../../api/profile';
import { formatDateTime } from '../../api/console';

// Same option sets as the legacy portal profile page.
const TIMEZONES = [
  'UTC',
  'Asia/Shanghai',
  'America/New_York',
  'Europe/London',
  'Asia/Tokyo',
  'Australia/Sydney',
  'America/Los_Angeles',
  'Europe/Berlin',
  'Asia/Singapore',
  'Asia/Dubai',
];

const CURRENCIES = [
  { value: 'USD', label: 'USD ($)' },
  { value: 'CNY', label: 'CNY (¥)' },
  { value: 'EUR', label: 'EUR (€)' },
  { value: 'GBP', label: 'GBP (£)' },
  { value: 'JPY', label: 'JPY (¥)' },
  { value: 'SGD', label: 'SGD (S$)' },
  { value: 'KRW', label: 'KRW (₩)' },
  { value: 'MYR', label: 'MYR (RM)' },
  { value: 'IDR', label: 'IDR (Rp)' },
  { value: 'THB', label: 'THB (฿)' },
  { value: 'VND', label: 'VND (₫)' },
  { value: 'TWD', label: 'TWD (NT$)' },
  { value: 'HKD', label: 'HKD (HK$)' },
  { value: 'MOP', label: 'MOP (MOP$)' },
  { value: 'AUD', label: 'AUD (A$)' },
  { value: 'NZD', label: 'NZD (NZ$)' },
  { value: 'RUB', label: 'RUB (₽)' },
];

export default function Profile() {
  const { t } = useI18n();
  const navigate = useNavigate();
  const [profile, setProfile] = useState<ProfileUser | null>(null);
  const [gdpr, setGdpr] = useState<GdprStatus | null>(null);
  const [currency, setCurrency] = useState<string>('USD');
  const [loading, setLoading] = useState(true);

  const [name, setName] = useState('');
  const [company, setCompany] = useState('');
  const [timezone, setTimezone] = useState('UTC');
  const [savingBasic, setSavingBasic] = useState(false);

  const [oldPassword, setOldPassword] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [savingPassword, setSavingPassword] = useState(false);

  const [exporting, setExporting] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [p, st, cur] = await Promise.all([getProfile(), getGdprStatus(), getCurrency()]);
      setProfile(p);
      setGdpr(st);
      setCurrency(cur || 'USD');
      setName(p.name ?? '');
      setCompany(p.company ?? '');
      setTimezone(p.timezone || 'UTC');
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    load();
  }, [load]);

  const saveBasic = async () => {
    const trimmed = name.trim();
    if (!trimmed) {
      Toast.warning(t('profile.nameRequired'));
      return;
    }
    setSavingBasic(true);
    try {
      await updateProfile({ name: trimmed, company: company.trim(), timezone });
      Toast.success(t('profile.saved'));
      setProfile((prev) =>
        prev ? { ...prev, name: trimmed, company: company.trim(), timezone } : prev
      );
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setSavingBasic(false);
    }
  };

  const onCurrencyChange = async (value: unknown) => {
    const next = String(value);
    try {
      await updateCurrency(next);
      setCurrency(next);
      Toast.success(t('profile.currencySaved'));
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    }
  };

  const savePassword = async () => {
    if (!oldPassword || !newPassword) {
      Toast.warning(t('profile.passwordRequired'));
      return;
    }
    if (newPassword.length < 8) {
      Toast.warning(t('profile.passwordTooShort'));
      return;
    }
    if (newPassword !== confirmPassword) {
      Toast.warning(t('profile.passwordMismatch'));
      return;
    }
    setSavingPassword(true);
    try {
      await changePassword(oldPassword, newPassword);
      Toast.success(t('profile.passwordChanged'));
      // token_version bumped server-side: current JWT is dead, force re-login.
      setOldPassword('');
      setNewPassword('');
      setConfirmPassword('');
      window.setTimeout(() => {
        setToken(null);
        navigate('/login', { replace: true });
      }, 800);
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
      setSavingPassword(false);
    }
  };

  const onExport = async () => {
    setExporting(true);
    try {
      const data = await exportMyData();
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `woodgit-data-export-${new Date().toISOString().slice(0, 10)}.json`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
      Toast.success(t('profile.exportSuccess'));
      setGdpr((prev) => ({ ...prev, gdpr_data_exported_at: new Date().toISOString() }));
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setExporting(false);
    }
  };

  const onDeleteAccount = () => {
    dangerConfirm({
      title: t('profile.deleteConfirmTitle'),
      content: t('profile.deleteConfirmContent'),
      okText: t('profile.deleteConfirmOk'),
      cancelText: t('common.cancel'),
      onOk: async () => {
        try {
          await requestDataDeletion();
          Toast.success(t('profile.deletionRequested'));
          // token_version bumped server-side: session is dead.
          window.setTimeout(() => {
            setToken(null);
            navigate('/login', { replace: true });
          }, 800);
        } catch (e) {
          Toast.error(e instanceof Error ? e.message : t('common.error'));
          throw e;
        }
      },
    });
  };

  const roleLabel = (role: string) => {
    const key = `profile.role.${role}`;
    const v = t(key);
    return v === key ? role : v;
  };

  return (
    <div className="wg-page">
      <PageHead title={t('acc.nav.profile')} />

      {loading || !profile ? (
        <div style={{ display: 'flex', justifyContent: 'center', padding: 80 }}>
          <Spin size="large" />
        </div>
      ) : (
        <>
          <section className="wg-pro-card">
            <div className="wg-pro-card-head">
              <span className="wg-pro-card-title">
                <IconUserStroked style={{ marginRight: 8 }} />
                {t('profile.section.basic')}
              </span>
            </div>
            <div className="wg-pro-card-body" style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
              <div className="wg-form-field" style={{ maxWidth: 420 }}>
                <label className="wg-form-label" htmlFor="profile-name">
                  {t('auth.name')}
                </label>
                <Input
                  id="profile-name"
                  value={name}
                  maxLength={64}
                  onChange={setName}
                  placeholder={t('profile.namePlaceholder')}
                />
              </div>
              <div className="wg-form-field" style={{ maxWidth: 420 }}>
                <label className="wg-form-label" htmlFor="profile-company">
                  {t('profile.company')}
                </label>
                <Input
                  id="profile-company"
                  value={company}
                  maxLength={128}
                  onChange={setCompany}
                  placeholder={t('profile.companyPlaceholder')}
                />
              </div>
              <div className="wg-form-field" style={{ maxWidth: 420 }}>
                <label className="wg-form-label" htmlFor="profile-timezone">
                  {t('profile.timezone')}
                </label>
                <Select
                  id="profile-timezone"
                  style={{ width: '100%' }}
                  value={timezone}
                  filter
                  onChange={(v) => setTimezone(String(v))}
                >
                  {TIMEZONES.map((tz) => (
                    <Select.Option key={tz} value={tz}>
                      {tz}
                    </Select.Option>
                  ))}
                </Select>
              </div>
              <div>
                <Button type="primary" theme="solid" loading={savingBasic} onClick={saveBasic}>
                  {t('common.save')}
                </Button>
              </div>
            </div>
          </section>

          <section className="wg-pro-card">
            <div className="wg-pro-card-head">
              <span className="wg-pro-card-title">
                <IconGlobeStroke style={{ marginRight: 8 }} />
                {t('profile.section.preferences')}
              </span>
            </div>
            <div className="wg-pro-card-body" style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
              <div className="wg-form-field" style={{ maxWidth: 420 }}>
                <label className="wg-form-label" htmlFor="profile-currency">
                  {t('profile.currency')}
                </label>
                <Select
                  id="profile-currency"
                  style={{ width: '100%' }}
                  value={currency}
                  onChange={onCurrencyChange}
                >
                  {CURRENCIES.map((c) => (
                    <Select.Option key={c.value} value={c.value}>
                      {c.label}
                    </Select.Option>
                  ))}
                </Select>
              </div>
            </div>
          </section>

          <ContactCard profile={profile} onChanged={setProfile} />

          <section className="wg-pro-card">
            <div className="wg-pro-card-head">
              <span className="wg-pro-card-title">
                <IconLockStroked style={{ marginRight: 8 }} />
                {t('profile.section.security')}
              </span>
            </div>
            <div className="wg-pro-card-body" style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
              <p className="wg-cell-dim" style={{ margin: 0 }}>
                {t('profile.passwordHint')}
              </p>
              <div className="wg-form-field" style={{ maxWidth: 420 }}>
                <label className="wg-form-label" htmlFor="profile-old-password">
                  {t('profile.currentPassword')}
                </label>
                <Input
                  id="profile-old-password"
                  type="password"
                  autoComplete="current-password"
                  value={oldPassword}
                  onChange={setOldPassword}
                />
              </div>
              <div className="wg-form-field" style={{ maxWidth: 420 }}>
                <label className="wg-form-label" htmlFor="profile-new-password">
                  {t('auth.newPassword')}
                </label>
                <Input
                  id="profile-new-password"
                  type="password"
                  autoComplete="new-password"
                  value={newPassword}
                  onChange={setNewPassword}
                  placeholder={t('auth.newPasswordPlaceholder')}
                />
              </div>
              <div className="wg-form-field" style={{ maxWidth: 420 }}>
                <label className="wg-form-label" htmlFor="profile-confirm-password">
                  {t('profile.confirmPassword')}
                </label>
                <Input
                  id="profile-confirm-password"
                  type="password"
                  autoComplete="new-password"
                  value={confirmPassword}
                  onChange={setConfirmPassword}
                />
              </div>
              <div>
                <Button type="primary" theme="solid" loading={savingPassword} onClick={savePassword}>
                  {t('profile.updatePassword')}
                </Button>
              </div>
            </div>
          </section>

          <MfaCard />

          <section className="wg-pro-card">
            <div className="wg-pro-card-head">
              <span className="wg-pro-card-title">
                <IconDownloadStroked style={{ marginRight: 8 }} />
                {t('profile.section.data')}
              </span>
            </div>
            <div className="wg-pro-card-body" style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
              <div
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'space-between',
                  gap: 16,
                  flexWrap: 'wrap',
                }}
              >
                <div>
                  <div style={{ marginBottom: 4 }}>{t('profile.exportData')}</div>
                  <div className="wg-cell-dim" style={{ fontSize: 12 }}>
                    {t('profile.dataExportHint')}
                    {gdpr?.gdpr_data_exported_at
                      ? ` ${t('profile.exportedAt')}: ${formatDateTime(gdpr.gdpr_data_exported_at)}`
                      : ` ${t('profile.neverExported')}`}
                  </div>
                </div>
                <Button icon={<IconDownloadStroked />} loading={exporting} onClick={onExport}>
                  {t('profile.exportData')}
                </Button>
              </div>
              <div
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'space-between',
                  gap: 16,
                  flexWrap: 'wrap',
                  borderTop: '1px solid var(--wg-border, rgba(128,128,128,0.2))',
                  paddingTop: 16,
                }}
              >
                <div>
                  <div style={{ marginBottom: 4 }}>{t('profile.deleteAccount')}</div>
                  <div className="wg-cell-dim" style={{ fontSize: 12 }}>
                    {t('profile.deleteHint')}
                    {gdpr?.gdpr_erasure_requested_at
                      ? ` ${t('profile.deletionRequestedAt')}: ${formatDateTime(gdpr.gdpr_erasure_requested_at)}`
                      : ''}
                  </div>
                </div>
                <Button
                  icon={<IconDeleteStroked />}
                  type="danger"
                  theme="solid"
                  disabled={!!gdpr?.gdpr_erasure_requested_at}
                  onClick={onDeleteAccount}
                >
                  {t('profile.deleteAccount')}
                </Button>
              </div>
            </div>
          </section>

          <section className="wg-pro-card">
            <div className="wg-pro-card-head">
              <span className="wg-pro-card-title">{t('profile.section.account')}</span>
            </div>
            <div className="wg-pro-card-body">
              <div
                style={{
                  display: 'grid',
                  gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))',
                  gap: 20,
                }}
              >
                <div className="wg-form-field">
                  <span className="wg-form-label">{t('auth.email')}</span>
                  <span>{profile.email || '—'}</span>
                </div>
                <div className="wg-form-field">
                  <span className="wg-form-label">{t('profile.role')}</span>
                  <span>
                    <Tag color="blue" size="small">
                      {roleLabel(profile.role)}
                    </Tag>
                  </span>
                </div>
                <div className="wg-form-field">
                  <span className="wg-form-label">{t('profile.registeredAt')}</span>
                  <span className="wg-num">{formatDateTime(profile.created_at)}</span>
                </div>
              </div>
            </div>
          </section>
        </>
      )}
    </div>
  );
}
