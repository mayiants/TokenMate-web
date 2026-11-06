import { useCallback, useEffect, useState } from 'react';
import {
  Button,
  Card,
  Descriptions,
  Empty,
  Progress,
  Spin,
  Table,
  Tabs,
  Toast,
} from '@douyinfe/semi-ui';
import PageHead from '../../components/PageHead';
import { useI18n } from '../../i18n';
import { useEnterprise } from './EntLayout';
import {
  getEnterpriseBillingDetails,
  getEnterpriseReconciliation,
  type EnterpriseBillingDetails,
  type EnterpriseReconciliation,
} from '../../api/enterprise';
import { formatUSD } from '../../api/console';
import { getToken } from '../../api/client';

async function downloadCsv(path: string, filename: string) {
  const res = await fetch(path, { headers: { Authorization: `Bearer ${getToken() ?? ''}` } });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}

export default function EntBilling() {
  const { t } = useI18n();
  const { info } = useEnterprise();
  const [details, setDetails] = useState<EnterpriseBillingDetails | null>(null);
  const [recon, setRecon] = useState<EnterpriseReconciliation | null>(null);
  const [tab, setTab] = useState('details');

  const load = useCallback(async () => {
    try {
      setDetails(await getEnterpriseBillingDetails());
    } catch {
      setDetails({});
    }
  }, []);
  const loadRecon = useCallback(async () => {
    try {
      setRecon(await getEnterpriseReconciliation());
    } catch {
      setRecon({});
    }
  }, []);

  useEffect(() => {
    load();
    loadRecon();
  }, [load, loadRecon]);

  const credit = details?.credit;
  const creditTotal = Number(credit?.total ?? info.credit_total ?? 0);
  const creditUsed = Number(credit?.used ?? info.credit_used ?? 0);
  const percent = creditTotal > 0 ? Math.min(100, Math.round((creditUsed / creditTotal) * 100)) : 0;

  const reconMembers = (recon?.members ?? []) as Array<Record<string, unknown>>;
  const reconColumns = reconMembers.length
    ? Object.keys(reconMembers[0]).slice(0, 8).map((key) => ({
        title: key.replace(/_/g, ' '),
        dataIndex: key,
        render: (v: unknown) =>
          v === null || v === undefined ? '—' : typeof v === 'object' ? JSON.stringify(v).slice(0, 60) : String(v),
      }))
    : [];

  const invoices = Array.isArray(details?.invoices) ? details!.invoices : [];

  return (
    <div className="wg-console-stack">
      <PageHead title={t('ent.nav.billing')} />
      <Card bordered title={t('ent.billing.creditPool')}>
        {details === null ? (
          <Spin />
        ) : (
          <div className="wg-field-grid">
            <div className="wg-credit-row">
              <div>
                <div className="wg-stat-label">{t('ent.billing.used')}</div>
                <div className="wg-stat-value">{formatUSD(creditUsed)}</div>
              </div>
              <div>
                <div className="wg-stat-label">{t('ent.billing.total')}</div>
                <div className="wg-stat-value">{formatUSD(creditTotal)}</div>
              </div>
              <div>
                <div className="wg-stat-label">{t('ent.billing.remaining')}</div>
                <div className="wg-stat-value">{formatUSD(Math.max(0, creditTotal - creditUsed))}</div>
              </div>
            </div>
            <Progress percent={percent} showInfo aria-label={t('ent.billing.creditPool')} />
            <Descriptions
              size="small"
              data={[
                { key: t('ent.billing.cycle'), value: details?.billing_cycle ?? '—' },
                { key: t('ent.billing.nextReset'), value: details?.next_reset_at ?? '—' },
                { key: t('ent.overview.currency'), value: details?.display_currency ?? 'USD' },
              ]}
            />
          </div>
        )}
      </Card>

      <Card bordered>
        <Tabs type="line" activeKey={tab} onChange={(k) => setTab(k as string)}>
          <Tabs.TabPane itemKey="details" tab={t('ent.billing.details')}>
            {invoices.length === 0 ? (
              <Empty description={t('ent.billing.noInvoices')} />
            ) : (
              <Table
                rowKey={(_: unknown, i?: number) => String(i)}
                columns={Object.keys(invoices[0] as object).slice(0, 8).map((key) => ({
                  title: key.replace(/_/g, ' '),
                  dataIndex: key,
                  render: (v: unknown) => (v === null || undefined ? '—' : String(v as string)),
                }))}
                dataSource={invoices as Array<Record<string, unknown>>}
                pagination={false}
              />
            )}
          </Tabs.TabPane>
          <Tabs.TabPane itemKey="reconciliation" tab={t('ent.billing.reconciliation')}>
            <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 12 }}>
              <Button
                onClick={() =>
                  downloadCsv('/api/enterprise/reconciliation/export', 'reconciliation.csv').catch((err) =>
                    Toast.error((err as Error).message)
                  )
                }
              >
                {t('common.export')}
              </Button>
            </div>
            {recon === null ? (
              <Spin />
            ) : reconMembers.length === 0 ? (
              <Empty description={t('common.emptyData')} />
            ) : (
              <Table rowKey="id" columns={reconColumns} dataSource={reconMembers} pagination={false} />
            )}
          </Tabs.TabPane>
        </Tabs>
      </Card>
    </div>
  );
}
