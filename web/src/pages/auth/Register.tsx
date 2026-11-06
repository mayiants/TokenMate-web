import { useRef, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { Button, Form, Toast } from '@douyinfe/semi-ui';
import type { FormApi } from '@douyinfe/semi-ui/lib/es/form';
import { IconLock, IconMail, IconUser, IconSend } from '@douyinfe/semi-icons';
import AuthCard from './AuthCard';
import { useAuth } from '../../auth/AuthContext';
import { sendCode } from '../../api/auth';
import { useI18n } from '../../i18n';

const COUNTDOWN_SECONDS = 60;

interface RegisterFormValues {
  name?: string;
  email: string;
  code: string;
  password: string;
  confirm: string;
}

export default function Register() {
  const { t } = useI18n();
  const { register } = useAuth();
  const navigate = useNavigate();
  const formApiRef = useRef<FormApi<RegisterFormValues> | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [sending, setSending] = useState(false);
  const [countdown, setCountdown] = useState(0);

  const startCountdown = () => {
    setCountdown(COUNTDOWN_SECONDS);
    const timer = window.setInterval(() => {
      setCountdown((prev) => {
        if (prev <= 1) {
          window.clearInterval(timer);
          return 0;
        }
        return prev - 1;
      });
    }, 1000);
  };

  const onSendCode = async () => {
    const email = formApiRef.current?.getValue('email')?.trim() ?? '';
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) {
      Toast.warning(t('auth.emailInvalid'));
      return;
    }
    setSending(true);
    try {
      await sendCode(email);
      Toast.success(t('auth.codeSent'));
      startCountdown();
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setSending(false);
    }
  };

  const onSubmit = async (values: RegisterFormValues) => {
    setSubmitting(true);
    try {
      await register({
        name: values.name?.trim() || undefined,
        email: values.email.trim(),
        password: values.password,
        code: values.code.trim(),
      });
      Toast.success(t('auth.registerSuccess'));
      navigate('/console', { replace: true });
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <AuthCard title={t('auth.registerTitle')} subtitle={t('auth.registerSubtitle')}>
      <Form
        className="wg-auth-form"
        getFormApi={(api) => {
          formApiRef.current = api as FormApi<RegisterFormValues>;
        }}
        onSubmit={onSubmit as never}
      >
        <Form.Input field="name" label={t('auth.name')} prefix={<IconUser />} placeholder={t('auth.namePlaceholder')} />
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
          field="code"
          label={t('auth.code')}
          placeholder={t('auth.codePlaceholder')}
          rules={[{ required: true, message: t('auth.codeRequired') }]}
          suffix={
            <Button
              theme="borderless"
              size="small"
              icon={<IconSend size="small" />}
              disabled={countdown > 0}
              loading={sending}
              onClick={onSendCode}
            >
              {countdown > 0 ? t('auth.codeResend').replace('{n}', String(countdown)) : t('auth.sendCode')}
            </Button>
          }
        />
        <Form.Input
          field="password"
          label={t('auth.password')}
          mode="password"
          prefix={<IconLock />}
          placeholder={t('auth.passwordPlaceholder')}
          rules={[
            { required: true, message: t('auth.passwordRequired') },
            { min: 8, message: t('auth.passwordTooShort') },
          ]}
        />
        <Form.Input
          field="confirm"
          label={t('auth.confirmPassword')}
          mode="password"
          prefix={<IconLock />}
          placeholder={t('auth.confirmPlaceholder')}
          rules={[
            { required: true, message: t('auth.confirmRequired') },
            {
              validator: (_rule, value: string) => {
                const pwd = formApiRef.current?.getValue('password');
                return pwd && value !== pwd ? new Error(t('auth.passwordMismatch')) : true;
              },
            },
          ]}
        />
        <Button block htmlType="submit" type="primary" theme="solid" loading={submitting} className="wg-auth-submit">
          {t('auth.registerButton')}
        </Button>
      </Form>
      <div className="wg-auth-switch">
        {t('auth.hasAccount')}
        <Link to="/login">{t('auth.toLogin')}</Link>
      </div>
    </AuthCard>
  );
}
