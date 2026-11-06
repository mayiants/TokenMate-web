import { useCallback, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button, Input, Spin, Tag, Toast } from '@douyinfe/semi-ui';
import { IconLockStroked, IconUserStroked } from '@douyinfe/semi-icons';
import PageHead from '../../components/PageHead';
import { useI18n } from '../../i18n';
import { setToken } from '../../api/client';
import { changePassword, getProfile, updateProfile, type ProfileUser } from '../../api/profile';
import { formatDateTime } from '../../api/console';

export default function Profile() {
  const { t } = useI18n();
  const navigate = useNavigate();
  const [profile, setProfile] = useState<ProfileUser | null>(null);
  const [loading, setLoading] = useState(true);

  const [name, setName] = useState('');
  const [savingName, setSavingName] = useState(false);

  const [oldPassword, setOldPassword] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [savingPassword, setSavingPassword] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const p = await getProfile();
      setProfile(p);
      setName(p.name ?? '');
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    load();
  }, [load]);

  const saveName = async () => {
    const trimmed = name.trim();
    if (!trimmed) {
      Toast.warning(t('profile.nameRequired'));
      return;
    }
    if (trimmed === profile?.name) return;
    setSavingName(true);
    try {
      await updateProfile({ name: trimmed });
      Toast.success(t('profile.saved'));
      setProfile((prev) => (prev ? { ...prev, name: trimmed } : prev));
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setSavingName(false);
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
              <div>
                <Button type="primary" theme="solid" loading={savingName} onClick={saveName}>
                  {t('common.save')}
                </Button>
              </div>
            </div>
          </section>

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
