import { useCallback, useEffect, useState } from 'react';
import { Button, Input, Spin, Tag, Toast } from '@douyinfe/semi-ui';
import { IconCopy, IconShieldStroked } from '@douyinfe/semi-icons';
import { useI18n } from '../../i18n';
import { ApiError, setToken } from '../../api/client';
import {
  beginMfaSetup,
  disableMfa,
  enableMfa,
  getMfaStatus,
  regenerateRecoveryCodes,
  type MfaSetup,
  type MfaStatus,
} from '../../api/profile';
import { formatDateTime } from '../../api/console';

/**
 * Two-factor authentication card for /console/profile (parity with the legacy
 * portal profile page: TOTP setup, recovery codes, disable, regenerate).
 * Enable/disable rotates the session token, so a returned token replaces the
 * stored one to keep this device signed in.
 */
export default function MfaCard() {
  const { t } = useI18n();
  const [status, setStatus] = useState<MfaStatus | null>(null);
  const [loading, setLoading] = useState(true);
  // Backends predating the MFA rollout answer 404 — hide the card rather than
  // surfacing an error (the same build must work against old and new portals).
  const [supported, setSupported] = useState(true);

  // setup flow
  const [setup, setSetup] = useState<MfaSetup | null>(null);
  const [setupCode, setSetupCode] = useState('');
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null);
  const [busy, setBusy] = useState(false);

  // disable / regenerate
  const [password, setPassword] = useState('');
  const [factorCode, setFactorCode] = useState('');
  const [disabling, setDisabling] = useState(false);
  const [regenerating, setRegenerating] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setStatus(await getMfaStatus());
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) {
        setSupported(false);
        return;
      }
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    load();
  }, [load]);

  if (!supported) return null;

  const copy = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      Toast.success(t('common.copied'));
    } catch {
      Toast.error(t('common.copyFailed'));
    }
  };

  const startSetup = async () => {
    setBusy(true);
    try {
      const res = await beginMfaSetup();
      setSetup(res);
      setSetupCode('');
      setRecoveryCodes(null);
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setBusy(false);
    }
  };

  const confirmEnable = async () => {
    const code = setupCode.trim();
    if (!code) {
      Toast.warning(t('auth.mfaCodeRequired'));
      return;
    }
    setBusy(true);
    try {
      const res = await enableMfa(code);
      if (res.token) setToken(res.token);
      setRecoveryCodes(res.recovery_codes ?? []);
      setSetup(null);
      setSetupCode('');
      Toast.success(t('profile.mfa.enabledToast'));
      await load();
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setBusy(false);
    }
  };

  const onDisable = async () => {
    if (!password) {
      Toast.warning(t('profile.mfa.passwordRequired'));
      return;
    }
    if (!factorCode.trim()) {
      Toast.warning(t('profile.mfa.codeRequired'));
      return;
    }
    setDisabling(true);
    try {
      const res = await disableMfa({ password, code: factorCode.trim() });
      if (res.token) setToken(res.token);
      setPassword('');
      setFactorCode('');
      setRecoveryCodes(null);
      Toast.success(t('profile.mfa.disabledToast'));
      await load();
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setDisabling(false);
    }
  };

  const onRegenerate = async () => {
    if (!password || !factorCode.trim()) {
      Toast.warning(t('profile.mfa.passwordAndCodeRequired'));
      return;
    }
    setRegenerating(true);
    try {
      const res = await regenerateRecoveryCodes(password, factorCode.trim());
      setRecoveryCodes(res.recovery_codes ?? []);
      setPassword('');
      setFactorCode('');
      Toast.success(t('profile.mfa.regeneratedToast'));
      await load();
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setRegenerating(false);
    }
  };

  return (
    <section className="wg-pro-card">
      <div className="wg-pro-card-head">
        <span className="wg-pro-card-title">
          <IconShieldStroked style={{ marginRight: 8 }} />
          {t('profile.mfa.title')}
        </span>
        {status?.enabled && (
          <Tag color="green" size="small">
            {t('profile.mfa.statusOn')}
          </Tag>
        )}
      </div>
      <div className="wg-pro-card-body" style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
        {loading ? (
          <Spin />
        ) : (
          <>
            <p className="wg-cell-dim" style={{ margin: 0 }}>
              {t('profile.mfa.desc')}
              {status?.required ? ` ${t('profile.mfa.policyRequired')}` : ''}
            </p>

            {recoveryCodes && recoveryCodes.length > 0 && (
              <div
                style={{
                  border: '1px solid var(--wg-border, rgba(128,128,128,0.25))',
                  borderRadius: 8,
                  padding: 16,
                  display: 'flex',
                  flexDirection: 'column',
                  gap: 8,
                }}
              >
                <div style={{ fontWeight: 600 }}>{t('profile.mfa.recoveryTitle')}</div>
                <div className="wg-cell-dim" style={{ fontSize: 12 }}>
                  {t('profile.mfa.recoveryHint')}
                </div>
                <div
                  className="wg-num"
                  style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(140px, 1fr))', gap: 6 }}
                >
                  {recoveryCodes.map((c) => (
                    <code key={c}>{c}</code>
                  ))}
                </div>
                <div>
                  <Button icon={<IconCopy />} size="small" onClick={() => copy(recoveryCodes.join('\n'))}>
                    {t('profile.mfa.copyRecovery')}
                  </Button>
                </div>
              </div>
            )}

            {status?.enabled ? (
              <>
                <div className="wg-form-field">
                  <span className="wg-form-label">{t('profile.mfa.enabledAt')}</span>
                  <span className="wg-num">{formatDateTime(status.enabled_at ?? undefined)}</span>
                </div>
                <div className="wg-form-field">
                  <span className="wg-form-label">{t('profile.mfa.recoveryRemaining')}</span>
                  <span className="wg-num">{status.recovery_codes_remaining}</span>
                </div>
                <div className="wg-form-field" style={{ maxWidth: 420 }}>
                  <label className="wg-form-label" htmlFor="mfa-password">
                    {t('profile.currentPassword')}
                  </label>
                  <Input
                    id="mfa-password"
                    type="password"
                    autoComplete="current-password"
                    value={password}
                    onChange={setPassword}
                  />
                </div>
                <div className="wg-form-field" style={{ maxWidth: 420 }}>
                  <label className="wg-form-label" htmlFor="mfa-code">
                    {t('profile.mfa.codeLabel')}
                  </label>
                  <Input
                    id="mfa-code"
                    value={factorCode}
                    onChange={setFactorCode}
                    placeholder={t('auth.mfaCodePlaceholder')}
                    autoComplete="one-time-code"
                  />
                </div>
                <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
                  <Button loading={regenerating} onClick={onRegenerate}>
                    {t('profile.mfa.regenerate')}
                  </Button>
                  <Button type="danger" theme="light" loading={disabling} onClick={onDisable}>
                    {t('profile.mfa.disable')}
                  </Button>
                </div>
              </>
            ) : setup ? (
              <>
                <div className="wg-form-field">
                  <span className="wg-form-label">{t('profile.mfa.secret')}</span>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                    <code className="wg-num">{setup.secret}</code>
                    <Button icon={<IconCopy />} size="small" theme="borderless" onClick={() => copy(setup.secret)}>
                      {t('common.copy')}
                    </Button>
                  </div>
                </div>
                <div className="wg-form-field">
                  <span className="wg-form-label">{t('profile.mfa.otpauth')}</span>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                    <code className="wg-cell-ellipsis" style={{ maxWidth: 420 }}>
                      {setup.otpauth_uri}
                    </code>
                    <Button icon={<IconCopy />} size="small" theme="borderless" onClick={() => copy(setup.otpauth_uri)}>
                      {t('common.copy')}
                    </Button>
                  </div>
                </div>
                <div className="wg-cell-dim" style={{ fontSize: 12 }}>
                  {t('profile.mfa.setupHint', {
                    issuer: setup.issuer,
                    digits: setup.digits,
                    period: setup.period,
                    algorithm: setup.algorithm,
                  })}
                </div>
                <div className="wg-form-field" style={{ maxWidth: 320 }}>
                  <label className="wg-form-label" htmlFor="mfa-setup-code">
                    {t('profile.mfa.codeLabel')}
                  </label>
                  <Input
                    id="mfa-setup-code"
                    value={setupCode}
                    onChange={setSetupCode}
                    placeholder={t('auth.mfaCodePlaceholder')}
                    autoComplete="one-time-code"
                  />
                </div>
                <div style={{ display: 'flex', gap: 12 }}>
                  <Button type="primary" theme="solid" loading={busy} onClick={confirmEnable}>
                    {t('profile.mfa.confirm')}
                  </Button>
                  <Button
                    theme="light"
                    onClick={() => {
                      setSetup(null);
                      setSetupCode('');
                    }}
                  >
                    {t('common.cancelAction')}
                  </Button>
                </div>
              </>
            ) : (
              <div>
                <Button type="primary" theme="solid" loading={busy} onClick={startSetup}>
                  {t('profile.mfa.enable')}
                </Button>
              </div>
            )}
          </>
        )}
      </div>
    </section>
  );
}
