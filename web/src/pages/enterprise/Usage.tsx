import { useCallback, useEffect, useState } from 'react';
import { Button, Card, Empty, Pagination, Spin, Table, Tabs, Toast } from '@douyinfe/semi-ui';
import PageHead from '../../components/PageHead';
import { useI18n } from '../../i18n';
import { useEnterprise } from './EntLayout';
import {
  getEnterpriseUsageDetails,
  getEnterpriseMemberLogs,
  getEnterpriseUsageErrors,
  getEnterpriseRateLimitHits,
  type PagedUsageRecords,
} from '../../api/enterprise';
import { getToken } from '../../api/client';

// Live response shapes for these endpoints vary; derive columns from the data.
type AnyRecord = Record<string, unknown>;
type Endpoint = (p: { page: number; pageSize: number }) => Promise<PagedUsageRecords>;

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

function deriveColumns(records: AnyRecord[]) {
  const first = records[0] ?? {};
  return Object.keys(first)
    .slice(0, 8)
    .map((key) => ({
      title: key.replace(/_/g, ' '),
      dataIndex: key,
      render: (v: unknown) => {
        if (v === null || v === undefined) return '—';
        if (typeof v === 'object') return <span className="wg-mono">{JSON.stringify(v).slice(0, 80)}</span>;
        return String(v);
      },
    }));
}

export default function EntUsage() {
  const { t } = useI18n();
  const { isAdmin } = useEnterprise();
  const [tab, setTab] = useState('details');
  const [data, setData] = useState<PagedUsageRecords | null>(null);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(false);
  const PAGE_SIZE = 10;

  const endpoint: Endpoint =
    tab === 'details'
      ? (p) => getEnterpriseUsageDetails({ page: p.page, pageSize: p.pageSize })
      : tab === 'member-logs'
        ? (p) => getEnterpriseMemberLogs({ page: p.page, pageSize: p.pageSize })
        : tab === 'errors'
          ? () => getEnterpriseUsageErrors()
          : () => getEnterpriseRateLimitHits();

  const load = useCallback(
    async (p: number) => {
      setLoading(true);
      try {
        setData(await endpoint({ page: p, pageSize: PAGE_SIZE }));
      } catch {
        setData({ records: [], total: 0 });
      } finally {
        setLoading(false);
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [tab]
  );

  useEffect(() => {
    setPage(1);
    load(1);
  }, [load]);

  const records = (data?.records ?? []) as AnyRecord[];
  const total = Number(data?.total ?? records.length ?? 0);

  const tabs = [
    { itemKey: 'details', tab: t('ent.usage.details') },
    ...(isAdmin ? [{ itemKey: 'member-logs', tab: t('ent.usage.memberLogs') }] : []),
    { itemKey: 'errors', tab: t('ent.usage.errors') },
    { itemKey: 'rate-limits', tab: t('ent.usage.rateLimits') },
  ];

  return (
    <div className="wg-console-stack">
      <PageHead title={t('ent.nav.usage')} />
      <Card
      bordered
      headerExtraContent={
        <Button
          onClick={() =>
            downloadCsv('/api/enterprise/usage/export', 'usage.csv').catch((e) =>
              Toast.error((e as Error).message)
            )
          }
        >
          {t('common.export')}
        </Button>
      }
    >
      <Tabs type="line" activeKey={tab} onChange={(k) => setTab(k as string)}>
        {tabs.map((tb) => (
          <Tabs.TabPane key={tb.itemKey} itemKey={tb.itemKey} tab={tb.tab} />
        ))}
      </Tabs>
      {loading ? (
        <Spin />
      ) : records.length === 0 ? (
        <Empty description={t('common.emptyData')} />
      ) : (
        <>
          <Table
            rowKey={(r?: AnyRecord) => String(r?.id ?? JSON.stringify(r ?? {}).slice(0, 24))}
            columns={deriveColumns(records)}
            dataSource={records}
            pagination={false}
          />
          {total > PAGE_SIZE && (
            <Pagination
              currentPage={page}
              total={total}
              pageSize={PAGE_SIZE}
              onPageChange={(p) => {
                setPage(p);
                load(p);
              }}
              style={{ marginTop: 12 }}
            />
          )}
        </>
      )}
    </Card>
    </div>
  );
}
