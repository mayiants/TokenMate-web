import { useCallback, useEffect, useMemo, useState } from 'react';
import { Button, DatePicker, Empty, Input, Modal, Popover, Select, Table, Tag, Toast, Typography } from '@douyinfe/semi-ui';
import {
  IconCopyStroked,
  IconDeleteStroked,
  IconEyeOpened,
  IconPlusCircle,
  IconRefresh,
  IconSearch,
} from '@douyinfe/semi-icons';
import KeyDrawer from '../../components/KeyDrawer';
import PageHead from '../../components/PageHead';
import { useI18n } from '../../i18n';
import { copyText } from '../../utils/format';
import { listPublicModels } from '../../api/models';
import {
  deleteKey,
  getKey,
  listKeys,
  maskKeyDisplay,
  updateKey,
  type ApiKey,
} from '../../api/keys';
import { formatDateTime, formatNumber } from '../../api/console';

const PAGE_SIZE_OPTIONS = [10, 20, 50];

function pad(n: number): string {
  return String(n).padStart(2, '0');
}

function toDateStr(d: Date): string {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

interface AppliedFilters {
  q?: string;
  status?: string;
  start?: string;
  end?: string;
}

export default function ApiKeys() {
  const { t } = useI18n();
  const [keys, setKeys] = useState<ApiKey[]>([]);
  const [loading, setLoading] = useState(true);

  // draft filter values vs. applied (query button)
  const [search, setSearch] = useState('');
  const [statusFilter, setStatusFilter] = useState<string>();
  const [createdRange, setCreatedRange] = useState<[Date, Date] | null>(null);
  const [applied, setApplied] = useState<AppliedFilters>({});

  const [selectedIds, setSelectedIds] = useState<string[]>([]);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(10);
  const [modelOptions, setModelOptions] = useState<string[]>([]);
  const [drawer, setDrawer] = useState<{ visible: boolean; mode: 'create' | 'edit'; keyData?: ApiKey | null }>({
    visible: false,
    mode: 'create',
  });
  const [revealed, setRevealed] = useState<Map<string, string>>(new Map());
  const [revealing, setRevealing] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const list = await listKeys();
      setKeys(list);
      setSelectedIds((prev) => prev.filter((id) => list.some((k) => k.id === id)));
    } catch {
      // auth failures redirect globally
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  useEffect(() => {
    listPublicModels()
      .then((models) => setModelOptions(models.map((m) => m.api_model_name).filter(Boolean)))
      .catch(() => setModelOptions([]));
  }, []);

  const filtered = useMemo(() => {
    let list = keys;
    const q = applied.q?.trim().toLowerCase();
    if (q) {
      list = list.filter((k) => k.name.toLowerCase().includes(q) || (k.key_prefix ?? '').toLowerCase().includes(q));
    }
    if (applied.status) {
      list = list.filter((k) => (applied.status === 'active' ? k.status === 'active' : k.status !== 'active'));
    }
    if (applied.start) {
      list = list.filter((k) => k.created_at && toDateStr(new Date(k.created_at)) >= applied.start!);
    }
    if (applied.end) {
      list = list.filter((k) => k.created_at && toDateStr(new Date(k.created_at)) <= applied.end!);
    }
    return list;
  }, [keys, applied]);

  const totalPages = Math.max(1, Math.ceil(filtered.length / pageSize));
  const safePage = Math.min(page, totalPages);
  const pageRows = filtered.slice((safePage - 1) * pageSize, safePage * pageSize);
  const from = filtered.length === 0 ? 0 : (safePage - 1) * pageSize + 1;
  const to = Math.min(safePage * pageSize, filtered.length);

  const onSearch = () => {
    setApplied({
      q: search || undefined,
      status: statusFilter,
      start: createdRange ? toDateStr(createdRange[0]) : undefined,
      end: createdRange ? toDateStr(createdRange[1]) : undefined,
    });
    setPage(1);
  };

  const onReset = () => {
    setSearch('');
    setStatusFilter(undefined);
    setCreatedRange(null);
    setApplied({});
    setPage(1);
  };

  const onCopy = async (text: string) => {
    const ok = await copyText(text);
    Toast[ok ? 'success' : 'error'](ok ? t('common.copied') : t('common.copyFailed'));
  };

  const onReveal = async (record: ApiKey) => {
    if (revealed.has(record.id)) return;
    setRevealing(record.id);
    try {
      const full = await getKey(record.id, true);
      if (full.key) {
        setRevealed((prev) => new Map(prev).set(record.id, full.key ?? ''));
      } else {
        Toast.warning(t('keys.revealUnavailable'));
      }
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setRevealing(null);
    }
  };

  const onToggleStatus = (record: ApiKey) => {
    const enable = record.status !== 'active';
    updateKey(record.id, { status: enable ? 'active' : 'inactive' })
      .then(() => {
        Toast.success(enable ? t('keys.enabled') : t('keys.disabled'));
        load();
      })
      .catch((e) => Toast.error(e instanceof Error ? e.message : t('common.error')));
  };

  const doDelete = async (ids: string[]) => {
    try {
      await Promise.all(ids.map((id) => deleteKey(id)));
      Toast.success(t('keys.deleted'));
      setSelectedIds([]);
      load();
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    }
  };

  const onDelete = (record: ApiKey) => {
    Modal.confirm({
      title: t('keys.deleteTitle'),
      content: t('keys.deleteConfirm').replace('{name}', record.name),
      okText: t('common.delete'),
      okButtonProps: { type: 'danger' },
      cancelText: t('common.cancel'),
      width: 480,
      className: 'wg-modal',
      onOk: () => doDelete([record.id]),
    });
  };

  const onBatchDelete = () => {
    Modal.confirm({
      title: t('keys.batchDeleteTitle'),
      content: t('keys.batchDeleteConfirm').replace('{n}', String(selectedIds.length)),
      okText: t('common.delete'),
      okButtonProps: { type: 'danger' },
      cancelText: t('common.cancel'),
      width: 480,
      className: 'wg-modal',
      onOk: () => doDelete(selectedIds),
    });
  };

  const keyCell = (record: ApiKey) => {
    const full = revealed.get(record.id);
    return (
      <div className="wg-cell-key">
      <PageHead title={t('console.nav.keys')} />
        <code className="wg-mono-sm">{full ?? maskKeyDisplay(record.key_prefix)}</code>
        <button type="button" className="wg-icon-btn" title={t('common.copy')} onClick={() => onCopy(full ?? record.key_prefix)}>
          <IconCopyStroked size="small" />
        </button>
        {!full && (
          <Popover content={t('keys.clickToView')} position="top">
            <button
              type="button"
              className="wg-icon-btn"
              title={t('keys.clickToView')}
              onClick={() => onReveal(record)}
            >
              <IconEyeOpened size="small" className={revealing === record.id ? 'wg-spin' : undefined} />
            </button>
          </Popover>
        )}
      </div>
    );
  };

  const limitsCell = (record: ApiKey) => {
    const rpm = record.rate_limit_rpm ?? 0;
    const tpm = record.rate_limit_tpm ?? 0;
    if (!rpm && !tpm) return <span className="wg-cell-dim">{t('keys.limits.default')}</span>;
    return (
      <span className="wg-num">
        {formatNumber(rpm)} · {formatNumber(tpm)}
      </span>
    );
  };

  const modelsCell = (record: ApiKey) => {
    const models = record.allowed_models ?? [];
    const text = models.length ? models.join(', ') : t('keys.allModels');
    return (
      <div className="wg-cell-models">
        <span className="wg-cell-ellipsis" title={text}>
          {models.length ? `${models.length} ` : ''}
          {models.length ? t('keys.modelsCount') : t('keys.allModels')}
        </span>
        <button type="button" className="wg-icon-btn" title={t('common.copy')} onClick={() => onCopy(text)}>
          <IconCopyStroked size="small" />
        </button>
      </div>
    );
  };

  const columns = [
    {
      title: t('keys.col.name'),
      dataIndex: 'name',
      width: 150,
      render: (_: unknown, r: ApiKey) => <span className="wg-cell-strong">{r.name}</span>,
    },
    { title: t('keys.col.key'), dataIndex: 'key_prefix', width: 250, render: (_: unknown, r: ApiKey) => keyCell(r) },
    {
      title: t('keys.col.status'),
      dataIndex: 'status',
      width: 96,
      render: (_: unknown, r: ApiKey) => (
        <Tag color={r.status === 'active' ? 'green' : 'red'} size="small" shape="circle">
          {r.status === 'active' ? t('keys.status.enabled') : t('keys.status.disabled')}
        </Tag>
      ),
    },
    {
      title: t('keys.col.limits'),
      dataIndex: 'rate_limit_rpm',
      width: 150,
      render: (_: unknown, r: ApiKey) => limitsCell(r),
    },
    { title: t('keys.col.models'), dataIndex: 'allowed_models', width: 140, render: (_: unknown, r: ApiKey) => modelsCell(r) },
    {
      title: t('keys.col.createdAt'),
      dataIndex: 'created_at',
      width: 165,
      render: (_: unknown, r: ApiKey) => <span className="wg-cell-dim wg-num">{formatDateTime(r.created_at)}</span>,
    },
    {
      title: t('keys.col.expiration'),
      dataIndex: 'expires_at',
      width: 165,
      render: (_: unknown, r: ApiKey) => (
        <span className="wg-cell-dim wg-num">{r.expires_at ? formatDateTime(r.expires_at) : t('keys.neverExpires')}</span>
      ),
    },
    {
      title: t('keys.col.action'),
      dataIndex: 'action',
      width: 170,
      render: (_: unknown, r: ApiKey) => (
        <div className="wg-cell-actions">
          <Typography.Text
            link
            onClick={() => setDrawer({ visible: true, mode: 'edit', keyData: r })}
          >
            {t('keys.action.edit')}
          </Typography.Text>
          <Typography.Text link type={r.status === 'active' ? 'warning' : 'success'} onClick={() => onToggleStatus(r)}>
            {r.status === 'active' ? t('keys.action.disable') : t('keys.action.enable')}
          </Typography.Text>
          <Typography.Text link type="danger" onClick={() => onDelete(r)}>
            {t('keys.action.delete')}
          </Typography.Text>
        </div>
      ),
    },
  ];

  return (
    <div className="wg-page">
      <div className="wg-page-head">
        <div>
          <h2 className="wg-page-title">{t('console.nav.keys')}</h2>
        </div>
      </div>

      <div className="wg-pro-card wg-filter-card">
        <Input
          style={{ width: 220 }}
          prefix={<IconSearch />}
          placeholder={t('keys.search')}
          value={search}
          onChange={setSearch}
          showClear
        />
        <Select
          style={{ width: 140 }}
          placeholder={t('keys.col.status')}
          optionList={[
            { label: t('keys.status.enabled'), value: 'active' },
            { label: t('keys.status.disabled'), value: 'inactive' },
          ]}
          value={statusFilter}
          onChange={(v) => setStatusFilter(v as string | undefined)}
          showClear
        />
        <DatePicker
          type="dateRange"
          style={{ width: 240 }}
          placeholder={[t('dash.range.start'), t('dash.range.end')]}
          value={createdRange ?? undefined}
          onChange={(v) => setCreatedRange((v as [Date, Date] | null) ?? null)}
        />
        <div className="wg-filter-actions">
          <Button type="primary" theme="solid" icon={<IconSearch />} onClick={onSearch}>
            {t('logs.search')}
          </Button>
          <Button theme="light" icon={<IconRefresh />} onClick={onReset}>
            {t('logs.reset')}
          </Button>
        </div>
      </div>

      <div className="wg-pro-card wg-table-card">
        <div className="wg-table-toolbar">
          <div className="wg-table-toolbar-left">
            <Button
              type="primary"
              theme="solid"
              icon={<IconPlusCircle />}
              onClick={() => setDrawer({ visible: true, mode: 'create' })}
            >
              {t('keys.create')}
            </Button>
            <Button
              type="danger"
              theme="light"
              icon={<IconDeleteStroked />}
              disabled={selectedIds.length === 0}
              onClick={onBatchDelete}
            >
              {t('keys.deleteSelected').replace('{n}', selectedIds.length ? ` (${selectedIds.length})` : '')}
            </Button>
          </div>
        </div>
        <Table
          loading={loading}
          columns={columns}
          dataSource={pageRows}
          pagination={false}
          rowKey="id"
          tableLayout="fixed"
          rowSelection={{
            selectedRowKeys: selectedIds,
            onChange: (selectedRowKeys) => setSelectedIds(selectedRowKeys as string[]),
          }}
          empty={
            <Empty
              description={applied.q || applied.status || applied.start ? t('keys.emptySearch') : t('keys.empty')}
            />
          }
        />
        <div className="wg-table-footer">
          <span className="wg-cell-dim">
            {t('keys.showing').replace('{from}', String(from)).replace('{to}', String(to)).replace('{total}', String(filtered.length))}
          </span>
          <div className="wg-table-footer-right">
            <Select<number>
              size="small"
              value={pageSize}
              onChange={(v) => {
                setPageSize((v as number | undefined) ?? 10);
                setPage(1);
              }}
              optionList={PAGE_SIZE_OPTIONS.map((n) => ({ label: `${t('keys.show')} ${n}`, value: n }))}
              style={{ width: 110 }}
            />
            <div className="wg-table-pager">
              <Button size="small" theme="light" disabled={safePage <= 1} onClick={() => setPage(safePage - 1)}>
                ‹
              </Button>
              <span className="wg-num">
                {safePage} / {totalPages}
              </span>
              <Button size="small" theme="light" disabled={safePage >= totalPages} onClick={() => setPage(safePage + 1)}>
                ›
              </Button>
            </div>
          </div>
        </div>
      </div>

      <KeyDrawer
        visible={drawer.visible}
        mode={drawer.mode}
        keyData={drawer.keyData}
        modelOptions={modelOptions}
        onClose={() => setDrawer((d) => ({ ...d, visible: false }))}
        onSaved={load}
      />
    </div>
  );
}
