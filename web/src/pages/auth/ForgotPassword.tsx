import { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { Button, Form, Toast } from '@douyinfe/semi-ui';
import { IconKey, IconLock, IconMail } from '@douyinfe/semi-icons';
import AuthCard from './AuthCard';
import { useI18n } from '../../i18n';
import { forgotPassword, resetPassword } from '../../api/auth';

/** Two-step reset: request a code by email, then set a new password.
 *  Backend is anti-enumeration (same success message either way). */
export default function ForgotPassword() {
  const { t } = useI18n();
  const navigate = useNavigate();
  const [step, setStep] = useState<'request' | 'reset'>('request');
  const [email, setEmail] = useState('');
  const [sending, setSending] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  const requestCode = async (values: { email: string }) => {
    setSending(true);
    try {
      await forgotPassword(values.email.trim());
      setEmail(values.email.trim());
      setStep('reset');
      Toast.success(t('auth.resetCodeSent'));
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setSending(false);
    }
  };

  const submitReset = async (values: { code: string; password: string }) => {
    setSubmitting(true);
    try {
      await resetPassword({ email, code: values.code.trim(), password: values.password });
      Toast.success(t('auth.resetDone'));
      navigate('/login', { replace: true });
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <AuthCard title={t('auth.forgotTitle')} subtitle={t('auth.forgotSubtitle')}>
      {step === 'request' ? (
        <Form className="wg-auth-form" onSubmit={requestCode as never}>
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
          <Button type="primary" theme="solid" htmlType="submit" loading={sending} block>
            {t('auth.sendResetCode')}
          </Button>
        </Form>
      ) : (
        <Form className="wg-auth-form" onSubmit={submitReset as never}>
          <Form.Input field="email" label={t('auth.email')} initValue={email} disabled prefix={<IconMail />} />
          <Form.Input
            field="code"
            label={t('auth.resetCode')}
            prefix={<IconKey />}
            placeholder={t('auth.resetCodePlaceholder')}
            rules={[{ required: true, message: t('auth.resetCodeRequired') }]}
          />
          <Form.Input
            field="password"
            label={t('auth.newPassword')}
            mode="password"
            prefix={<IconLock />}
            placeholder={t('auth.newPasswordPlaceholder')}
            rules={[
              { required: true, message: t('auth.passwordRequired') },
              { min: 8, message: t('auth.passwordTooShort') },
            ]}
          />
          <Button type="primary" theme="solid" htmlType="submit" loading={submitting} block>
            {t('auth.resetSubmit')}
          </Button>
        </Form>
      )}
      <div className="wg-auth-links">
        <Link to="/login">{t('auth.backToLogin')}</Link>
      </div>
    </AuthCard>
  );
}
