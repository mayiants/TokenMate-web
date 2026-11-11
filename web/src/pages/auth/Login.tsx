import { useState } from 'react';
import { Link, useLocation, useNavigate } from 'react-router-dom';
import { Button, Form, Input, Toast } from '@douyinfe/semi-ui';
import { IconLock, IconMail, IconShieldStroked } from '@douyinfe/semi-icons';
import AuthCard from './AuthCard';
import { useAuth } from '../../auth/AuthContext';
import { useI18n } from '../../i18n';

export default function Login() {
  const { t } = useI18n();
  const { login, loginMfa } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const [submitting, setSubmitting] = useState(false);

  // Second factor: the password step only yields an mfa_token when the account
  // (or its MFA policy) requires TOTP, so the challenge lives in local state
  // rather than on its own route — a refresh simply restarts the login.
  const [mfaToken, setMfaToken] = useState<string | null>(null);
  const [useRecovery, setUseRecovery] = useState(false);
  const [code, setCode] = useState('');

  const from = (location.state as { from?: string } | null)?.from ?? '/console';

  const onSubmit = async (values: { email: string; password: string }) => {
    setSubmitting(true);
    try {
      const res = await login(values.email.trim(), values.password);
      if (res.status === 'mfa') {
        setMfaToken(res.mfaToken);
        setCode('');
        return;
      }
      Toast.success(t('auth.loginSuccess'));
      navigate(from, { replace: true });
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setSubmitting(false);
    }
  };

  const onVerify = async () => {
    if (!mfaToken) return;
    const value = code.trim();
    if (!value) {
      Toast.warning(useRecovery ? t('auth.mfaRecoveryRequired') : t('auth.mfaCodeRequired'));
      return;
    }
    setSubmitting(true);
    try {
      await loginMfa(mfaToken, useRecovery ? { recoveryCode: value } : { code: value });
      Toast.success(t('auth.loginSuccess'));
      navigate(from, { replace: true });
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setSubmitting(false);
    }
  };

  if (mfaToken) {
    return (
      <AuthCard
        title={t('auth.mfaTitle')}
        subtitle={useRecovery ? t('auth.mfaRecoverySubtitle') : t('auth.mfaSubtitle')}
      >
        <div className="wg-auth-form">
          <div className="wg-form-field">
            <label className="wg-form-label" htmlFor="mfa-code">
              {useRecovery ? t('auth.mfaRecoveryCode') : t('auth.mfaCode')}
            </label>
            <Input
              id="mfa-code"
              prefix={<IconShieldStroked />}
              placeholder={
                useRecovery ? t('auth.mfaRecoveryPlaceholder') : t('auth.mfaCodePlaceholder')
              }
              value={code}
              onChange={setCode}
              autoComplete="one-time-code"
            />
          </div>          <Button
            block
            type="primary"
            theme="solid"
            loading={submitting}
            className="wg-auth-submit"
            onClick={onVerify}
          >
            {t('auth.mfaVerify')}
          </Button>
          <div className="wg-auth-switch" style={{ marginTop: 4 }}>
            <Button
              theme="borderless"
              size="small"
              onClick={() => {
                setUseRecovery((v) => !v);
                setCode('');
              }}
            >
              {useRecovery ? t('auth.mfaUseCode') : t('auth.mfaUseRecovery')}
            </Button>
          </div>
          <div className="wg-auth-switch">
            <Button
              theme="borderless"
              size="small"
              onClick={() => {
                setMfaToken(null);
                setCode('');
                setUseRecovery(false);
              }}
            >
              {t('auth.mfaBack')}
            </Button>
          </div>
        </div>
      </AuthCard>
    );
  }

  return (
    <AuthCard title={t('auth.loginTitle')} subtitle={t('auth.loginSubtitle')}>
      <Form className="wg-auth-form" onSubmit={onSubmit as never}>
        <Form.Input
          field="email"
          label={t('auth.email')}
          prefix={<IconMail />}
          placeholder={t('auth.emailPlaceholder')}
          rules={[
            { required: true, message: t('auth.emailRequired') },
            { type: 'email', message: t('auth.emailInvalid') },
          ]}
        />
        <Form.Input
          field="password"
          label={t('auth.password')}
          mode="password"
          prefix={<IconLock />}
          placeholder={t('auth.passwordPlaceholder')}
          rules={[{ required: true, message: t('auth.passwordRequired') }]}
        />
        <div style={{ textAlign: 'right', marginBottom: 8 }}>
          <Link to="/forgot-password" style={{ fontSize: 13 }}>{t('auth.forgotLink')}</Link>
        </div>
        <Button block htmlType="submit" type="primary" theme="solid" loading={submitting} className="wg-auth-submit">
          {t('nav.login')}
        </Button>
      </Form>
      <div className="wg-auth-switch">
        {t('auth.noAccount')}
        <Link to="/register">{t('auth.toRegister')}</Link>
      </div>
    </AuthCard>
  );
}
