import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Button,
  Empty,
  Select,
  SideSheet,
  Spin,
  Switch,
  Table,
  Tag,
  Toast,
} from '@douyinfe/semi-ui';
import { IconAlertTriangle, IconRefresh } from '@douyinfe/semi-icons';
import PageHead from '../../components/PageHead';
import DrawerSections from '../../components/DrawerSections';
import { useI18n } from '../../i18n';
import {
  acknowledgeEnterpriseAlert,
  getEnterpriseAlertSettings,
  listEnterpriseAlerts,
  updateEnterpriseAlertSetting,
  type EnterpriseAlert,
  type EnterpriseAlertSetting,
} from '../../api/enterprise';

type Status = 'all' | 'open' | 'acknowledged';
type Level = 'info' | 'warning' | 'critical';

const LEVEL_TAG: Record<Level, 'blue' | 'orange' | 'red'> = {
  info: 'blue',
  warning: 'orange',
  critical: 'red',
};

function levelOf(alertType: string): Level {
  if (alertType.endsWith('_exceeded')) return 'critical';
  if (alertType.endsWith('_warning')) return 'warning';
  return 'info';
}

function metricOf(alertType: string): 'quota' | 'credit' {
  return alertType.startsWith('credit') ? 'credit' : 'quota';
}

function pad(n: number): string {
  return String(n).padStart(2, '0');
}

function formatTime(iso: string | null | undefined): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return String(iso);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function formatNum(v: number | null | undefined): string {
  if (v === null || v === undefined || Number.isNaN(Number(v))) return '—';
  return Number(v).toLocaleString(undefined, { maximumFractionDigits: 2 });
}

export default function EntAlerts() {
  const { t } = useI18n();

  const [settings, setSettings] = useState<EnterpriseAlertSetting[] | null>(null);
  const [alerts, setAlerts] = useState<EnterpriseAlert[] | null>(null);
  const [status, setStatus] = useState<Status>('all');
  const [editing, setEditing] = useState<EnterpriseAlertSetting | null>(null);
  const [draftEnabled, setDraftEnabled] = useState(true);
  const [savingKey, setSavingKey] = useState<string | null>(null);
  const [savingRule, setSavingRule] = useState(false);
  const [ackingId, setAckingId] = useState<string | null>(null);

  const loadSettings = useCallback(async () => {
    try {
      setSettings(await getEnterpriseAlertSettings());
    } catch (e) {
      setSettings([]);
      Toast.error((e as Error).message);
    }
  }, []);

  const loadAlerts = useCallback(async (s: Status) => {
    try {
      setAlerts(await listEnterpriseAlerts({ status: s, limit: 100 }));
    } catch {
      setAlerts([]);
    }
  }, []);

  useEffect(() => {
    loadSettings();
  }, [loadSettings]);

  useEffect(() => {
    setAlerts(null);
    loadAlerts(status);
  }, [status, loadAlerts]);

  useEffect(() => {
    setDraftEnabled(editing?.enabled ?? true);
  }, [editing]);

  const ruleName = (alertType: string) => {
    const key = `ent.alerts.rule.${alertType}`;
    const label = t(key);
    return label === key ? alertType : label;
  };

  const ruleSummary = (rule: EnterpriseAlertSetting) => {
    const key = `ent.alerts.summary.${rule.alert_type}`;
    const label = t(key, { threshold: rule.threshold_percent });
    return label === key ? `${rule.alert_type} · ${rule.threshold_percent}%` : label;
  };

  const toggleRule = async (rule: EnterpriseAlertSetting, enabled: boolean) => {
    const key = `${rule.alert_type}:${rule.threshold_percent}`;
    setSavingKey(key);
    try {
      await updateEnterpriseAlertSetting({
        alert_type: rule.alert_type,
        threshold_percent: rule.threshold_percent,
        enabled,
      });
      setSettings((prev) =>
        prev ? prev.map((s) => (s === rule ? { ...s, enabled } : s)) : prev
      );
      Toast.success(enabled ? t('ent.alerts.enabledToast') : t('ent.alerts.disabledToast'));
    } catch (e) {
      Toast.error((e as Error).message);
    } finally {
      setSavingKey(null);
    }
  };

  const saveRule = async () => {
    if (!editing) return;
    setSavingRule(true);
    try {
      await updateEnterpriseAlertSetting({
        alert_type: editing.alert_type,
        threshold_percent: editing.threshold_percent,
        enabled: draftEnabled,
      });
      Toast.success(t('ent.alerts.saved'));
      setEditing(null);
      await loadSettings();
    } catch (e) {
      Toast.error((e as Error).message);
    } finally {
      setSavingRule(false);
    }
  };

  const acknowledge = async (alert: EnterpriseAlert) => {
    setAckingId(alert.id);
    try {
      await acknowledgeEnterpriseAlert(alert.id);
      Toast.success(t('ent.alerts.acked'));
      await loadAlerts(status);
    } catch (e) {
      Toast.error((e as Error).message);
    } finally {
      setAckingId(null);
    }
  };

  const eventContent = (alert: EnterpriseAlert) =>
    t(`ent.alerts.event.${metricOf(alert.alert_type)}`, {
      current: formatNum(alert.current_value),
      limit: formatNum(alert.limit_value),
      threshold: alert.threshold_percent,
    });

  const columns = [
    {
      title: t('ent.alerts.col.level'),
      dataIndex: 'alert_type',
      width: 110,
      render: (v: string) => {
        const level = levelOf(v);
        return (
          <Tag color={LEVEL_TAG[level]} size="small" shape="circle">
            {t(`ent.alerts.level.${level}`)}
          </Tag>
        );
      },
    },
    {
      title: t('ent.alerts.col.time'),
      dataIndex: 'notified_at',
      width: 160,
      render: (v: string) => <span className="wg-num wg-cell-dim">{formatTime(v)}</span>,
    },
    {
      title: t('ent.alerts.col.content'),
      key: 'content',
      render: (_: unknown, a: EnterpriseAlert) => (
        <span className="wg-cell-ellipsis" title={eventContent(a)}>
          {eventContent(a)}
        </span>
      ),
    },
    {
      title: t('ent.alerts.col.read'),
      dataIndex: 'acknowledged_at',
      width: 110,
      render: (v: string | null | undefined) =>
        v ? (
          <Tag color="green" size="small" shape="circle">
            {t('ent.alerts.read')}
          </Tag>
        ) : (
          <Tag color="grey" size="small" shape="circle">
            {t('ent.alerts.unread')}
          </Tag>
        ),
    },
    {
      title: t('common.actions'),
      width: 110,
      render: (_: unknown, a: EnterpriseAlert) =>
        a.acknowledged_at ? (
          <span className="wg-cell-dim">—</span>
        ) : (
          <Button
            theme="borderless"
            size="small"
            loading={ackingId === a.id}
            onClick={() => acknowledge(a)}
          >
            {t('ent.alerts.markRead')}
          </Button>
        ),
    },
  ];

  const ruleCount = useMemo(
    () => (settings ? settings.filter((s) => s.enabled).length : 0),
    [settings]
  );

  return (
    <div className="wg-console-stack">
      <PageHead title={t('ent.nav.alerts')} />

      <div className="wg-pro-card">
        <div className="wg-table-toolbar">
          <div className="wg-table-toolbar-left">
            <span className="wg-pro-card-title">{t('ent.alerts.rules')}</span>
            <span className="wg-cell-dim">{t('ent.alerts.rulesDesc')}</span>
          </div>
          {settings && (
            <span className="wg-cell-dim">
              {t('ent.alerts.rulesSub', { enabled: ruleCount, total: settings.length })}
            </span>
          )}
        </div>
        <div className="wg-pro-card-body">
          {settings === null ? (
            <Spin />
          ) : settings.length === 0 ? (
            <Empty description={t('ent.alerts.emptyRules')} />
          ) : (
            <div className="wg-alert-rules">
              {settings.map((rule) => {
                const key = `${rule.alert_type}:${rule.threshold_percent}`;
                const level = levelOf(rule.alert_type);
                return (
                  <div key={key} className="wg-alert-rule">
                    <div className="wg-alert-rule-head">
                      <span className="wg-alert-rule-name">
                        <span className={`wg-alert-rule-icon${level === 'critical' ? ' is-critical' : ''}`}>
                          <IconAlertTriangle size="small" />
                        </span>
                        {ruleName(rule.alert_type)}
                      </span>
                      <Switch
                        size="small"
                        checked={rule.enabled}
                        loading={savingKey === key}
                        disabled={!rule.configurable}
                        onChange={(v) => toggleRule(rule, v)}
                        aria-label={t('ent.alerts.toggleAria', {
                          rule: ruleName(rule.alert_type),
                          threshold: rule.threshold_percent,
                        })}
                      />
                    </div>
                    <p className="wg-alert-rule-desc">{ruleSummary(rule)}</p>
                    <div className="wg-alert-rule-foot">
                      <Tag color="grey" size="small">
                        {t('ent.alerts.thresholdValue', { threshold: rule.threshold_percent })}
                      </Tag>
                      <Button theme="borderless" size="small" onClick={() => setEditing(rule)}>
                        {t('common.edit')}
                      </Button>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </div>
      </div>

      <div className="wg-pro-card wg-table-card">
        <div className="wg-table-toolbar">
          <div className="wg-table-toolbar-left">
            <span className="wg-pro-card-title">{t('ent.alerts.events')}</span>
            <Select
              size="small"
              style={{ width: 140 }}
              value={status}
              onChange={(v) => setStatus((v as Status | undefined) ?? 'all')}
              optionList={[
                { value: 'all', label: t('ent.alerts.status.all') },
                { value: 'open', label: t('ent.alerts.status.open') },
                { value: 'acknowledged', label: t('ent.alerts.status.acknowledged') },
              ]}
            />
          </div>
          <Button
            theme="light"
            icon={<IconRefresh />}
            onClick={() => loadAlerts(status)}
          >
            {t('common.refresh')}
          </Button>
        </div>
        <Table
          loading={alerts === null}
          rowKey="id"
          columns={columns}
          dataSource={alerts ?? []}
          pagination={{ pageSize: 10, showTotal: true }}
          tableLayout="fixed"
          empty={<Empty description={t('ent.alerts.emptyEvents')} />}
        />
      </div>

      {editing && (
        <SideSheet
          title={t('ent.alerts.editTitle', { rule: ruleName(editing.alert_type) })}
          visible
          onCancel={() => setEditing(null)}
          width={560}
          closeOnEsc
          maskClosable={false}
          footer={
            <div className="wg-drawer-footer">
              <Button onClick={() => setEditing(null)}>{t('common.cancel')}</Button>
              <Button type="primary" theme="solid" loading={savingRule} onClick={saveRule}>
                {t('common.save')}
              </Button>
            </div>
          }
        >
          <DrawerSections
            sections={[
              {
                key: 'rule',
                title: t('ent.alerts.editSection'),
                desc: t('ent.alerts.editSectionDesc'),
                children: (
                  <div className="wg-field-grid">
                    <div>
                      <span className="wg-form-label">{t('ent.alerts.col.rule')}</span>
                      <div className="wg-cell-strong">{ruleName(editing.alert_type)}</div>
                    </div>
                    <div>
                      <span className="wg-form-label">{t('ent.alerts.threshold')}</span>
                      <div className="wg-cell-strong wg-num">
                        {t('ent.alerts.thresholdValue', { threshold: editing.threshold_percent })}
                      </div>
                    </div>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                      <span className="wg-form-label">{t('ent.alerts.enableRule')}</span>
                      <Switch checked={draftEnabled} onChange={setDraftEnabled} />
                    </div>
                  </div>
                ),
              },
            ]}
          />
          <p className="wg-cell-dim" style={{ marginTop: 16 }}>
            {t('ent.alerts.fixedHint')}
          </p>
        </SideSheet>
      )}
    </div>
  );
}
