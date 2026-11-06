import { useCallback, useEffect, useMemo, useState } from 'react';
import { Button, DatePicker, Empty, Input, Select, Table, Tag, Toast } from '@douyinfe/semi-ui';
import { IconExport, IconRefresh, IconSearch } from '@douyinfe/semi-icons';
import PageHead from '../../components/PageHead';
import { useI18n } from '../../i18n';
import { download } from '../../api/client';
import {
  enterpriseAuditExportPath,
  getEnterpriseAudit,
  type EnterpriseAuditEvent,
  type EnterpriseRoleAudit,
} from '../../api/enterprise';

const CATEGORIES = ['billing', 'member', 'key', 'group', 'settings', 'alert', 'role'] as const;
const PAGE_SIZE_OPTIONS = [10, 20, 50];

const CATEGORY_TAG: Record<string, 'green' | 'blue' | 'violet' | 'cyan' | 'grey' | 'orange' | 'red'> = {
  billing: 'green',
  member: 'blue',
  key: 'violet',
  group: 'cyan',
  settings: 'grey',
  alert: 'orange',
  role: 'violet',
};

interface AuditRow {
  key: string;
  time: string;
  operator: string;
  category: string;
  changeType: string;
  field: string;
  oldValue: string;
  newValue: string;
  reason: string;
}

interface AppliedFilters {
  category: string;
  start?: string;
  end?: string;
  operator?: string;
  changeType?: string;
}

function pad(n: number): string {
  return String(n).padStart(2, '0');
}

function toDateStr(d: Date): string {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

function formatValue(v: number | null | undefined): string {
  return v === null || v === undefined ? '—' : String(v);
}

function formatTime(iso: string | null | undefined): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return String(iso);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export default function EntAudit() {
  const { t } = useI18n();

  const [roles, setRoles] = useState<EnterpriseRoleAudit[]>([]);
  const [events, setEvents] = useState<EnterpriseAuditEvent[]>([]);
  const [loading, setLoading] = useState(true);
  const [exporting, setExporting] = useState(false);

  // draft controls vs. applied filters
  const [dateRange, setDateRange] = useState<[Date, Date] | null>(null);
  const [category, setCategory] = useState('all');
  const [operator, setOperator] = useState('');
  const [changeType, setChangeType] = useState('all');
  const [applied, setApplied] = useState<AppliedFilters>({ category: 'all' });

  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(10);

  const load = useCallback(async (cat: string) => {
    setLoading(true);
    try {
      const res = await getEnterpriseAudit(cat);
      setRoles(res.roles);
      setEvents(res.events);
    } catch (e) {
      setRoles([]);
      setEvents([]);
      Toast.error((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load(applied.category);
  }, [applied.category, load]);

  // Role rows and unified event rows share one table; role changes carry
  // a plain old_role -> new_role string pair instead of numeric values.
  const rows = useMemo<AuditRow[]>(() => {
    const out: AuditRow[] = [];
    for (const r of roles) {
      out.push({
        key: `role-${r.id}`,
        time: r.changed_at,
        operator: r.changed_by_email || 'system',
        category: 'role',
        changeType: 'role_changed',
        field: 'role',
        oldValue: r.old_role || '—',
        newValue: r.new_role || '—',
        reason: r.reason || '',
      });
    }
    for (const e of events) {
      out.push({
        key: `event-${e.id}`,
        time: e.created_at,
        operator: e.changed_by || 'system',
        category: e.category,
        changeType: e.change_type,
        field: e.field_name || '—',
        oldValue: formatValue(e.old_value),
        newValue: formatValue(e.new_value),
        reason: e.reason || '',
      });
    }
    return out.sort((a, b) => (a.time < b.time ? 1 : a.time > b.time ? -1 : 0));
  }, [roles, events]);

  // The list endpoint caps each row set at 50, so change-type options only
  // cover what was loaded — enough to narrow the visible page.
  const changeTypeOptions = useMemo(() => {
    const seen = new Set<string>();
    rows.forEach((r) => seen.add(r.changeType));
    return Array.from(seen)
      .sort()
      .map((v) => ({ value: v, label: v === 'role_changed' ? t('ent.audit.roleChanged') : v }));
  }, [rows, t]);

  const filtered = useMemo(() => {
    return rows.filter((r) => {
      const day = r.time.slice(0, 10);
      if (applied.start && day < applied.start) return false;
      if (applied.end && day > applied.end) return false;
      if (applied.operator && !r.operator.toLowerCase().includes(applied.operator.toLowerCase())) {
        return false;
      }
      if (applied.changeType && applied.changeType !== 'all' && r.changeType !== applied.changeType) {
        return false;
      }
      return true;
    });
  }, [rows, applied]);

  useEffect(() => {
    setPage(1);
  }, [applied, pageSize]);

  const total = filtered.length;
  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  const current = Math.min(page, totalPages);
  const paged = filtered.slice((current - 1) * pageSize, current * pageSize);
  const from = total === 0 ? 0 : (current - 1) * pageSize + 1;
  const to = Math.min(current * pageSize, total);

  const onSearch = () => {
    const categoryChanged = category !== applied.category;
    if (categoryChanged) setChangeType('all');
    setApplied({
      category,
      start: dateRange ? toDateStr(dateRange[0]) : undefined,
      end: dateRange ? toDateStr(dateRange[1]) : undefined,
      operator: operator.trim() || undefined,
      changeType: categoryChanged || changeType === 'all' ? undefined : changeType,
    });
  };

  const onReset = () => {
    setDateRange(null);
    setCategory('all');
    setOperator('');
    setChangeType('all');
    setApplied({ category: 'all' });
  };

  const onExport = async () => {
    setExporting(true);
    try {
      await download(
        enterpriseAuditExportPath({
          type: applied.category === 'role' ? 'roles' : 'credits',
          category: applied.category,
          from: applied.start,
          to: applied.end,
          operator: applied.operator,
        })
      );
      Toast.success(t('ent.audit.exported'));
    } catch (e) {
      Toast.error((e as Error).message);
    } finally {
      setExporting(false);
    }
  };

  const categoryLabel = (c: string) => {
    const key = `ent.audit.category.${c}`;
    const label = t(key);
    return label === key ? c : label;
  };

  const columns = [
    {
      title: t('ent.audit.col.time'),
      dataIndex: 'time',
      width: 160,
      render: (v: string) => <span className="wg-num wg-cell-dim">{formatTime(v)}</span>,
    },
    {
      title: t('ent.audit.col.operator'),
      dataIndex: 'operator',
      width: 190,
      render: (v: string) => (
        <span className="wg-cell-ellipsis" title={v === 'system' ? t('ent.audit.system') : v}>
          {v === 'system' ? t('ent.audit.system') : v || '—'}
        </span>
      ),
    },
    {
      title: t('ent.audit.col.category'),
      dataIndex: 'category',
      width: 110,
      render: (v: string) => (
        <Tag color={CATEGORY_TAG[v] ?? 'grey'} size="small" shape="circle">
          {categoryLabel(v)}
        </Tag>
      ),
    },
    {
      title: t('ent.audit.col.field'),
      dataIndex: 'field',
      width: 150,
      render: (v: string) => (
        <span className="wg-cell-ellipsis wg-mono-sm" title={v}>
          {v || '—'}
        </span>
      ),
    },
    {
      title: t('ent.audit.col.change'),
      key: 'change',
      width: 220,
      render: (_: unknown, r: AuditRow) => (
        <span className="wg-mono-sm" title={`${r.oldValue} → ${r.newValue}`}>
          {r.oldValue} <span className="wg-cell-dim">→</span> {r.newValue}
        </span>
      ),
    },
    {
      title: t('ent.audit.col.reason'),
      dataIndex: 'reason',
      render: (v: string) => (
        <span className="wg-cell-ellipsis" title={v}>
          {v || '—'}
        </span>
      ),
    },
  ];

  return (
    <div className="wg-console-stack">
      <PageHead title={t('ent.nav.audit')} />

      <div className="wg-pro-card wg-filter-card">
        <DatePicker
          type="dateRange"
          style={{ width: 240 }}
          placeholder={[t('dash.range.start'), t('dash.range.end')]}
          value={dateRange ?? undefined}
          onChange={(v) => setDateRange((v as [Date, Date] | null) ?? null)}
        />
        <Input
          style={{ width: 200 }}
          placeholder={t('ent.audit.filter.operator')}
          value={operator}
          onChange={setOperator}
          showClear
        />
        <Select
          style={{ width: 140 }}
          value={category}
          onChange={(v) => setCategory((v as string | undefined) ?? 'all')}
          optionList={[
            { value: 'all', label: t('ent.audit.category.all') },
            ...CATEGORIES.map((c) => ({ value: c, label: categoryLabel(c) })),
          ]}
        />
        <Select
          style={{ width: 180 }}
          value={changeType}
          onChange={(v) => setChangeType((v as string | undefined) ?? 'all')}
          optionList={[{ value: 'all', label: t('ent.audit.changeType.all') }, ...changeTypeOptions]}
        />
        <div className="wg-filter-actions">
          <Button type="primary" theme="solid" icon={<IconSearch />} onClick={onSearch}>
            {t('common.search')}
          </Button>
          <Button theme="light" icon={<IconRefresh />} onClick={onReset}>
            {t('common.reset')}
          </Button>
        </div>
      </div>

      <div className="wg-pro-card wg-table-card">
        <div className="wg-table-toolbar">
          <div className="wg-table-toolbar-left">
            <span className="wg-pro-card-title">{t('ent.audit.tableTitle')}</span>
          </div>
          <Button theme="light" icon={<IconExport />} loading={exporting} onClick={onExport}>
            {t('common.export')}
          </Button>
        </div>
        <Table
          loading={loading}
          rowKey="key"
          columns={columns}
          dataSource={paged}
          pagination={false}
          tableLayout="fixed"
          empty={<Empty description={t('ent.audit.empty')} />}
        />
        <div className="wg-table-footer">
          <span className="wg-cell-dim">
            {t('keys.showing')
              .replace('{from}', String(from))
              .replace('{to}', String(to))
              .replace('{total}', String(total))}
          </span>
          <div className="wg-table-footer-right">
            <Select<number>
              size="small"
              value={pageSize}
              onChange={(v) => setPageSize((v as number | undefined) ?? 10)}
              optionList={PAGE_SIZE_OPTIONS.map((n) => ({
                label: `${t('keys.show')} ${n}`,
                value: n,
              }))}
              style={{ width: 110 }}
            />
            <div className="wg-table-pager">
              <Button size="small" theme="light" disabled={current <= 1} onClick={() => setPage(current - 1)}>
                ‹
              </Button>
              <span className="wg-num">
                {current} / {totalPages}
              </span>
              <Button
                size="small"
                theme="light"
                disabled={current >= totalPages}
                onClick={() => setPage(current + 1)}
              >
                ›
              </Button>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
