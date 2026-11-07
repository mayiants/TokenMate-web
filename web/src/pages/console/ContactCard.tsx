import { useEffect, useRef, useState } from 'react';
import { Button, Input, Modal, Toast } from '@douyinfe/semi-ui';
import { IconMailStroked, IconPhoneStroke, IconShieldStroked } from '@douyinfe/semi-icons';
import { useI18n } from '../../i18n';
import {
  changeBackupEmail,
  changeEmail,
  changePhone,
  sendBackupEmailCode,
  sendChangeCode,
  type ProfileUser,
} from '../../api/profile';

type Kind = 'email' | 'phone' | 'backup';

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

/** Contact section of the profile page: email / phone / backup email with
 *  verification-code change flows (same contract as the legacy portal). */
export default function ContactCard({ profile, onChanged }: { profile: ProfileUser; onChanged: (p: ProfileUser) => void }) {
  const { t } = useI18n();
  const [open, setOpen] = useState<Kind | null>(null);

  const fieldLabel = (k: Kind) =>
    k === 'email' ? t('profile.contact.email') : k === 'phone' ? t('profile.contact.phone') : t('profile.contact.backupEmail');

  const current = (k: Kind) =>
    k === 'email' ? profile.email : k === 'phone' ? profile.phone || '' : profile.second_email || '';

  const row = (k: Kind, icon: React.ReactNode) => (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: 16,
        flexWrap: 'wrap',
        padding: '8px 0',
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, minWidth: 0 }}>
        <span style={{ color: 'var(--wg-text-2)', display: 'inline-flex' }}>{icon}</span>
        <span style={{ minWidth: 72 }}>{fieldLabel(k)}</span>
        <span className="wg-cell-dim" style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>
          {current(k) || '—'}
        </span>
      </div>
      <Button size="small" onClick={() => setOpen(k)}>
        {current(k) ? t('profile.contact.change') : t('profile.contact.set')}
      </Button>
    </div>
  );

  return (
    <section className="wg-pro-card">
      <div className="wg-pro-card-head">
        <span className="wg-pro-card-title">
          <IconMailStroked style={{ marginRight: 8 }} />
          {t('profile.contact.title')}
        </span>
      </div>
      <div className="wg-pro-card-body" style={{ display: 'flex', flexDirection: 'column' }}>
        {row('email', <IconMailStroked />)}
        {row('phone', <IconPhoneStroke />)}
        {row('backup', <IconShieldStroked />)}
      </div>
      {open !== null && (
        <ChangeModal
          kind={open}
          current={current(open)}
          onClose={() => setOpen(null)}
          onDone={(kind, value) => {
            if (kind === 'email') onChanged({ ...profile, email: value });
            else if (kind === 'phone') onChanged({ ...profile, phone: value });
            else onChanged({ ...profile, second_email: value });
            setOpen(null);
          }}
        />
      )}
    </section>
  );
}

function ChangeModal({
  kind,
  current,
  onClose,
  onDone,
}: {
  kind: Kind;
  current: string;
  onClose: () => void;
  onDone: (kind: Kind, value: string) => void;
}) {
  const { t } = useI18n();
  const [value, setValue] = useState('');
  const [code, setCode] = useState('');
  const [password, setPassword] = useState('');
  const [sending, setSending] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [cooldown, setCooldown] = useState(0);
  const timer = useRef<number | null>(null);

  useEffect(() => {
    return () => {
      if (timer.current) window.clearInterval(timer.current);
    };
  }, []);

  const field = kind === 'email' ? t('profile.contact.email') : kind === 'phone' ? t('profile.contact.phone') : t('profile.contact.backupEmail');
  const isEmail = kind !== 'phone';
  // SMS gateway is not integrated — phone changes cannot deliver codes.
  const phoneBlocked = kind === 'phone';

  const startCooldown = (seconds: number) => {
    setCooldown(seconds);
    if (timer.current) window.clearInterval(timer.current);
    timer.current = window.setInterval(() => {
      setCooldown((s) => {
        if (s <= 1 && timer.current) {
          window.clearInterval(timer.current);
          timer.current = null;
          return 0;
        }
        return s - 1;
      });
    }, 1000);
  };

  const sendCode = async () => {
    const v = value.trim();
    if (!v) {
      Toast.warning(t('profile.contact.valueRequired').replace('{field}', field));
      return;
    }
    if (isEmail && !EMAIL_RE.test(v)) {
      Toast.warning(t('profile.contact.emailInvalid'));
      return;
    }
    setSending(true);
    try {
      if (kind === 'backup') await sendBackupEmailCode(v);
      else await sendChangeCode(kind, v);
      Toast.success(t('profile.contact.codeSent'));
      startCooldown(60);
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setSending(false);
    }
  };

  const submit = async () => {
    const v = value.trim();
    if (!v) {
      Toast.warning(t('profile.contact.valueRequired').replace('{field}', field));
      return;
    }
    if (isEmail && !EMAIL_RE.test(v)) {
      Toast.warning(t('profile.contact.emailInvalid'));
      return;
    }
    if (!code.trim()) {
      Toast.warning(t('profile.contact.codeRequired'));
      return;
    }
    if (!password) {
      Toast.warning(t('profile.contact.passwordRequired'));
      return;
    }
    setSubmitting(true);
    try {
      if (kind === 'email') await changeEmail({ new_email: v, verification_code: code.trim(), password });
      else if (kind === 'phone') await changePhone({ new_phone: v, verification_code: code.trim(), password });
      else await changeBackupEmail({ new_email: v, verification_code: code.trim(), password });
      Toast.success(
        t(
          kind === 'email'
            ? 'profile.contact.emailChanged'
            : kind === 'phone'
              ? 'profile.contact.phoneChanged'
              : 'profile.contact.backupEmailChanged'
        )
      );
      onDone(kind, v);
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Modal
      title={`${current ? t('profile.contact.change') : t('profile.contact.set')}${field}`}
      visible
      onCancel={onClose}
      onOk={submit}
      okText={t('common.save')}
      cancelText={t('common.cancel')}
      confirmLoading={submitting}
      maskClosable={false}
      okButtonProps={{ disabled: phoneBlocked }}
    >
      <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
        {phoneBlocked && <p className="wg-cell-dim" style={{ margin: 0 }}>{t('profile.contact.phoneUnavailable')}</p>}
        <div className="wg-form-field">
          <label className="wg-form-label">{t('profile.contact.newValue').replace('{field}', field)}</label>
          <Input
            value={value}
            disabled={phoneBlocked}
            type={kind === 'phone' ? 'tel' : 'email'}
            placeholder={current || undefined}
            onChange={setValue}
          />
        </div>
        <div className="wg-form-field">
          <label className="wg-form-label">{t('profile.contact.code')}</label>
          <div style={{ display: 'flex', gap: 8 }}>
            <Input value={code} disabled={phoneBlocked} onChange={setCode} maxLength={8} style={{ flex: 1 }} />
            <Button disabled={phoneBlocked || cooldown > 0 || sending} loading={sending} onClick={sendCode}>
              {cooldown > 0
                ? t('profile.contact.codeWait').replace('{s}', String(cooldown))
                : t('profile.contact.sendCode')}
            </Button>
          </div>
        </div>
        <div className="wg-form-field">
          <label className="wg-form-label">{t('profile.contact.password')}</label>
          <Input
            value={password}
            type="password"
            autoComplete="current-password"
            disabled={phoneBlocked}
            onChange={setPassword}
          />
          <span className="wg-cell-dim" style={{ fontSize: 12 }}>{t('profile.contact.passwordHint')}</span>
        </div>
      </div>
    </Modal>
  );
}
