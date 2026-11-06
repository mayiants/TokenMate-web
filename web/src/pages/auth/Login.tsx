import { useState } from 'react';
import { Link, useLocation, useNavigate } from 'react-router-dom';
import { Button, Form, Toast } from '@douyinfe/semi-ui';
import { IconLock, IconMail } from '@douyinfe/semi-icons';
import AuthCard from './AuthCard';
import { useAuth } from '../../auth/AuthContext';
import { useI18n } from '../../i18n';

export default function Login() {
  const { t } = useI18n();
  const { login } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const [submitting, setSubmitting] = useState(false);

  const from = (location.state as { from?: string } | null)?.from ?? '/console';

  const onSubmit = async (values: { email: string; password: string }) => {
    setSubmitting(true);
    try {
      await login(values.email.trim(), values.password);
      Toast.success(t('auth.loginSuccess'));
      navigate(from, { replace: true });
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setSubmitting(false);
    }
  };

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
