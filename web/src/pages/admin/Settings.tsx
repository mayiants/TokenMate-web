import { useCallback, useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  Button,
  Empty,
  Input,
  InputNumber,
  Spin,
  Switch,
  Tag,
  Toast,
} from '@douyinfe/semi-ui';
import { IconArrowRight, IconMailStroked, IconRefresh } from '@douyinfe/semi-icons';
import PageHead from '../../components/PageHead';
import DrawerSections from '../../components/DrawerSections';
import dangerConfirm from '../../components/dangerConfirm';
import { useI18n } from '../../i18n';
import {
  deleteSmtpConfig,
  getAdminSettings,
  getSmtpConfig,
  resetAdminSettings,
  testSmtpConfig,
  updateAdminSetting,
  type AdminSettingsResponse,
  type SmtpConfig,
} from '../../api/adminSettings';

const PLATFORM_KEYS = [
  'default_user_rpm',
  'default_user_tpm',
  'default_user_daily_requests',
  'balance_low_threshold_usd',
  'payment_simulation_mode',
] as const;

const PRICING_KEYS = ['usd_exchange_rate'] as const;

function flattenSettings(res: AdminSettingsResponse | null): Record<string, string> {
  const out: Record<string, string> = { ...(res?.defaults ?? {}) };
  for (const group of Object.values(res?.settings ?? {})) {
    for (const [key, entry] of Object.entries(group.settings ?? {})) {
      if (entry?.value !== undefined) out[key] = entry.value;
    }
  }
  return out;
}

export default function AdminSettings() {
  const { t } = useI18n();
  const [data, setData] = useState<AdminSettingsResponse | null>(null);
  const [smtp, setSmtp] = useState<SmtpConfig | null>(null);
  const [loading, setLoading] = useState(true);
  const [form, setForm] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState<string | null>(null);
  const [testTo, setTestTo] = useState('');
  const [testing, setTesting] = useState(false);

  const initial = useMemo(() => flattenSettings(data), [data]);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [settings, smtpConfig] = await Promise.all([
        getAdminSettings(),
        getSmtpConfig().catch(() => null),
      ]);
      setData(settings);
      setForm(flattenSettings(settings));
      setSmtp(smtpConfig);
    } catch (e) {
      Toast.error((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const setValue = (key: string, value: string) => {
    setForm((prev) => ({ ...prev, [key]: value }));
  };

  const saveKeys = async (keys: readonly string[]) => {
    const changed = keys.filter((k) => (form[k] ?? '') !== (initial[k] ?? ''));
    if (changed.length === 0) {
      Toast.info(t('admin.settings.noChanges'));
      return;
    }
    setSaving(keys.join(','));
    try {
      for (const key of changed) {
        await updateAdminSetting(key, form[key] ?? '');
      }
      Toast.success(t('admin.common.saved'));
      await load();
    } catch (e) {
      Toast.error((e as Error).message);
    } finally {
      setSaving(null);
    }
  };

  const sendTest = async () => {
    if (!testTo.trim()) {
      Toast.error(t('admin.settings.mail.testRequired'));
      return;
    }
    setTesting(true);
    try {
      await testSmtpConfig(testTo.trim());
      Toast.success(t('admin.settings.mail.testSent'));
    } catch (e) {
      Toast.error((e as Error).message);
    } finally {
      setTesting(false);
    }
  };

  const smtpConfig = smtp?.config ?? {};

  return (
    <div className="wg-page">
      <PageHead
        title={t('admin.settings.title')}
        extra={
          <Button icon={<IconRefresh />} theme="light" onClick={load}>
            {t('common.refresh')}
          </Button>
        }
      />

      {loading && data === null ? (
        <div className="wg-page-loading">
          <Spin size="large" />
        </div>
      ) : (
        <DrawerSections
          sections={[
            {
              key: 'platform',
              title: t('admin.settings.platform.title'),
              desc: t('admin.settings.platform.desc'),
              tip: t('admin.settings.platform.tip'),
              children: (
                <div className="wg-field-grid">
                  <p className="wg-cell-dim" style={{ margin: 0 }}>
                    {t('admin.settings.platform.note')}
                  </p>

                  <NumberField
                    label={t('admin.settings.platform.rpm')}
                    value={form.default_user_rpm}
                    onChange={(v) => setValue('default_user_rpm', v)}
                    hint={t('admin.settings.platform.rpmHint')}
                  />

                  <NumberField
                    label={t('admin.settings.platform.tpm')}
                    value={form.default_user_tpm}
                    onChange={(v) => setValue('default_user_tpm', v)}
                    hint={t('admin.settings.platform.tpmHint')}
                  />

                  <NumberField
                    label={t('admin.settings.platform.daily')}
                    value={form.default_user_daily_requests}
                    onChange={(v) => setValue('default_user_daily_requests', v)}
                    hint={t('admin.settings.platform.dailyHint')}
                  />

                  <NumberField
                    label={t('admin.settings.platform.balanceThreshold')}
                    value={form.balance_low_threshold_usd}
                    onChange={(v) => setValue('balance_low_threshold_usd', v)}
                    precision={2}
                    suffix="USD"
                    hint={t('admin.settings.platform.balanceHint')}
                  />

                  <div className="wg-form-field">
                    <label className="wg-form-label">{t('admin.settings.platform.simulation')}</label>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                      <Switch
                        checked={form.payment_simulation_mode === 'true'}
                        onChange={(v) => setValue('payment_simulation_mode', v ? 'true' : 'false')}
                        aria-label={t('admin.settings.platform.simulation')}
                      />
                      <span className="wg-cell-dim">{t('admin.settings.platform.simulationHint')}</span>
                    </div>
                  </div>

                  <div>
                    <Button
                      type="primary"
                      theme="solid"
                      loading={saving === PLATFORM_KEYS.join(',')}
                      onClick={() => saveKeys(PLATFORM_KEYS)}
                    >
                      {t('common.save')}
                    </Button>
                  </div>
                </div>
              ),
            },
            {
              key: 'pricing',
              title: t('admin.settings.pricing.title'),
              desc: t('admin.settings.pricing.desc'),
              tip: t('admin.settings.pricing.tip'),
              children: (
                <div className="wg-field-grid">
                  <NumberField
                    label={t('admin.settings.pricing.exchangeRate')}
                    value={form.usd_exchange_rate}
                    onChange={(v) => setValue('usd_exchange_rate', v)}
                    precision={2}
                    hint={t('admin.settings.pricing.exchangeHint')}
                  />

                  <div className="wg-form-field">
                    <label className="wg-form-label">{t('admin.settings.pricing.ratio')}</label>
                    <p className="wg-cell-dim" style={{ margin: 0 }}>
                      {t('admin.settings.pricing.ratioNote')}
                    </p>
                    <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
                      <Link to="/console/admin/groups">
                        <Button theme="light" size="small" icon={<IconArrowRight />}>
                          {t('admin.settings.pricing.goGroups')}
                        </Button>
                      </Link>
                      <Link to="/console/admin/models">
                        <Button theme="light" size="small" icon={<IconArrowRight />}>
                          {t('admin.settings.pricing.goPricing')}
                        </Button>
                      </Link>
                    </div>
                  </div>

                  <div>
                    <Button
                      type="primary"
                      theme="solid"
                      loading={saving === PRICING_KEYS.join(',')}
                      onClick={() => saveKeys(PRICING_KEYS)}
                    >
                      {t('common.save')}
                    </Button>
                  </div>
                </div>
              ),
            },
            {
              key: 'mail',
              title: t('admin.settings.mail.title'),
              desc: t('admin.settings.mail.desc'),
              tip: t('admin.settings.mail.tip'),
              children: (
                <div className="wg-field-grid">
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <IconMailStroked />
                    <Tag color={smtp?.source === 'db' ? 'blue' : 'grey'}>
                      {t(`admin.settings.mail.source.${smtp?.source ?? 'env'}`)}
                    </Tag>
                    <Tag color={smtp?.has_password ? 'green' : 'red'}>
                      {t(smtp?.has_password ? 'admin.settings.mail.configured' : 'admin.settings.mail.notConfigured')}
                    </Tag>
                  </div>

                  <div
                    style={{
                      display: 'grid',
                      gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))',
                      gap: 14,
                    }}
                  >
                    <Meta label={t('admin.settings.mail.host')} value={smtpConfig.host || '—'} />
                    <Meta label={t('admin.settings.mail.port')} value={String(smtpConfig.port ?? '—')} />
                    <Meta
                      label={t('admin.settings.mail.secure')}
                      value={t(smtpConfig.secure ? 'admin.settings.mail.secureOn' : 'admin.settings.mail.secureOff')}
                    />
                    <Meta label={t('admin.settings.mail.user')} value={smtpConfig.user || '—'} />
                    <Meta label={t('admin.settings.mail.from')} value={smtpConfig.from || '—'} />
                  </div>

                  <div className="wg-form-field">
                    <label className="wg-form-label">{t('admin.settings.mail.testTo')}</label>
                    <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap' }}>
                      <Input
                        style={{ width: 280 }}
                        value={testTo}
                        onChange={setTestTo}
                        placeholder={t('admin.settings.mail.testPh')}
                      />
                      <Button type="primary" theme="solid" loading={testing} onClick={sendTest}>
                        {t('admin.settings.mail.sendTest')}
                      </Button>
                    </div>
                  </div>
                </div>
              ),
            },
            {
              key: 'danger',
              title: t('admin.settings.danger.title'),
              desc: t('admin.settings.danger.desc'),
              children: (
                <div className="wg-field-grid">
                  <DangerRow
                    title={t('admin.settings.danger.resetTitle')}
                    desc={t('admin.settings.danger.resetDesc')}
                    action={t('admin.settings.danger.resetAction')}
                    onOk={async () => {
                      try {
                        const keys = await resetAdminSettings();
                        Toast.success(t('admin.settings.danger.resetDone', { count: keys.length }));
                        await load();
                      } catch (e) {
                        Toast.error((e as Error).message);
                      }
                    }}
                  />

                  <DangerRow
                    title={t('admin.settings.danger.smtpTitle')}
                    desc={t('admin.settings.danger.smtpDesc')}
                    action={t('admin.settings.danger.smtpAction')}
                    onOk={async () => {
                      try {
                        await deleteSmtpConfig();
                        Toast.success(t('admin.settings.danger.smtpDone'));
                        await load();
                      } catch (e) {
                        Toast.error((e as Error).message);
                      }
                    }}
                  />

                  <p className="wg-cell-dim" style={{ margin: 0 }}>
                    {t('admin.settings.danger.cacheNote')}
                  </p>
                </div>
              ),
            },
          ]}
        />
      )}

      {!loading && data === null && <Empty description={t('common.error')} />}
    </div>
  );
}

function NumberField({
  label,
  value,
  onChange,
  hint,
  precision = 0,
  suffix,
}: {
  label: string;
  value?: string;
  onChange: (v: string) => void;
  hint?: string;
  precision?: number;
  suffix?: string;
}) {
  return (
    <div className="wg-form-field">
      <label className="wg-form-label">{label}</label>
      <InputNumber aria-label="numeric setting"
        style={{ width: 240 }}
        min={0}
        precision={precision}
        suffix={suffix}
        value={value === undefined || value === '' ? 0 : Number(value)}
        onChange={(v) => onChange(typeof v === 'number' ? String(v) : '')}
      />
      {hint && <span className="wg-cell-dim">{hint}</span>}
    </div>
  );
}

function Meta({ label, value }: { label: string; value: string }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
      <span className="wg-cell-dim">{label}</span>
      <span className="wg-cell-strong wg-cell-ellipsis" title={value}>
        {value}
      </span>
    </div>
  );
}

function DangerRow({
  title,
  desc,
  action,
  onOk,
}: {
  title: string;
  desc: string;
  action: string;
  onOk: () => Promise<void>;
}) {
  const { t } = useI18n();
  return (
    <div
      className="wg-pro-card"
      style={{
        padding: 16,
        display: 'flex',
        alignItems: 'center',
        gap: 16,
        borderColor: 'var(--semi-color-danger)',
        flexWrap: 'wrap',
      }}
    >
      <div style={{ flex: 1, minWidth: 220 }}>
        <div className="wg-cell-strong">{title}</div>
        <div className="wg-cell-dim">{desc}</div>
      </div>
      <Button
        type="danger"
        theme="light"
        onClick={() =>
          dangerConfirm({
            title,
            content: desc,
            okText: action,
            cancelText: t('common.cancelAction'),
            onOk,
          })
        }
      >
        {action}
      </Button>
    </div>
  );
}
