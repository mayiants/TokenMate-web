import { useCallback, useEffect, useState } from 'react';
import {
  Button,
  Empty,
  Input,
  Select,
  SideSheet,
  Switch,
  Table,
  Tabs,
  Tag,
  TextArea,
  Toast,
} from '@douyinfe/semi-ui';
import {
  IconPlus,
  IconRefresh,
  IconSearch,
} from '@douyinfe/semi-icons';
import PageHead from '../../components/PageHead';
import dangerConfirm from '../../components/dangerConfirm';
import { useI18n } from '../../i18n';
import { formatNumber, formatDateTime } from '../../api/console';
import { getAdminTicketStats } from '../../api/admin';
import {
  archiveAdminAnnouncement,
  createAdminAnnouncement,
  deleteAdminAnnouncement,
  getAdminAnnouncement,
  getAdminTicket,
  listAdminAnnouncements,
  listAdminTicketRows,
  patchAdminTicket,
  publishAdminAnnouncement,
  replyAdminTicket,
  updateAdminAnnouncement,
  type AdminAnnouncement,
  type AdminAnnouncementDetail,
  type AdminTicketDetail,
  type AdminTicketRow,
  type AnnouncementPayload,
} from '../../api/adminOps';

type TabKey = 'tickets' | 'announcements';

const PRIORITIES = ['low', 'normal', 'high', 'urgent'] as const;
const TICKET_STATUSES = ['open', 'pending', 'answered', 'resolved', 'closed'] as const;
const ANNOUNCEMENT_TYPES = ['info', 'warning', 'success'] as const;

export default function AdminOps() {
  const { t } = useI18n();
  const [tab, setTab] = useState<TabKey>('tickets');

  // tickets
  const [tickets, setTickets] = useState<AdminTicketRow[] | null>(null);
  const [ticketsLoading, setTicketsLoading] = useState(true);
  const [search, setSearch] = useState('');
  const [priority, setPriority] = useState('all');
  const [status, setStatus] = useState('all');
  const [stats, setStats] = useState<Record<string, number>>({});
  const [detailId, setDetailId] = useState<string | null>(null);

  // announcements
  const [announcements, setAnnouncements] = useState<AdminAnnouncement[] | null>(null);
  const [annLoading, setAnnLoading] = useState(false);
  const [annPage, setAnnPage] = useState(1);
  const [annTotal, setAnnTotal] = useState(0);
  const [annTotalPages, setAnnTotalPages] = useState(1);
  const [createOpen, setCreateOpen] = useState(false);
  const [editTarget, setEditTarget] = useState<AdminAnnouncementDetail | null>(null);
  const [loadingEdit, setLoadingEdit] = useState(false);

  const loadTickets = useCallback(
    async (p = priority, s = status, q = search) => {
      setTicketsLoading(true);
      try {
        const [list, st] = await Promise.all([
          listAdminTicketRows({
            priority: p === 'all' ? undefined : p,
            status: s === 'all' ? undefined : s,
            search: q.trim() || undefined,
          }),
          getAdminTicketStats().catch(() => ({})),
        ]);
        setTickets(list);
        setStats(st);
      } catch (e) {
        setTickets([]);
        Toast.error((e as Error).message);
      } finally {
        setTicketsLoading(false);
      }
    },
    [priority, status, search]
  );

  const loadAnnouncements = useCallback(async (page = annPage) => {
    setAnnLoading(true);
    try {
      const res = await listAdminAnnouncements(page, 20);
      setAnnouncements(res.announcements);
      setAnnTotal(res.total);
      setAnnTotalPages(res.total_pages);
      setAnnPage(res.page);
    } catch (e) {
      setAnnouncements([]);
      Toast.error((e as Error).message);
    } finally {
      setAnnLoading(false);
    }
  }, [annPage]);

  useEffect(() => {
    loadTickets();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (tab === 'announcements' && announcements === null) loadAnnouncements(1);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab]);

  const openEdit = async (row: AdminAnnouncement) => {
    setLoadingEdit(true);
    try {
      setEditTarget(await getAdminAnnouncement(row.id));
    } catch (e) {
      Toast.error((e as Error).message);
    } finally {
      setLoadingEdit(false);
    }
  };

  const changeStatus = async (row: AdminTicketRow, next: 'resolved' | 'closed') => {
    try {
      await patchAdminTicket(row.id, { status: next });
      Toast.success(t(`admin.ops.ticket.${next}Done`));
      await loadTickets();
      if (detailId === row.id) setDetailId(null);
    } catch (e) {
      Toast.error((e as Error).message);
    }
  };

  const ticketColumns = [
    {
      title: t('admin.ops.ticket.col.no'),
      dataIndex: 'ticket_no',
      width: 90,
      render: (v: number) => <span className="wg-num">#{v ?? '—'}</span>,
    },
    {
      title: t('admin.ops.ticket.col.subject'),
      dataIndex: 'subject',
      render: (v: string, r: AdminTicketRow) => (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          <span className="wg-cell-strong wg-cell-ellipsis" title={v}>
            {v || '—'}
          </span>
          <span className="wg-cell-dim">{r.requester_email || '—'}</span>
        </div>
      ),
    },
    {
      title: t('admin.ops.ticket.col.category'),
      dataIndex: 'category',
      width: 110,
      render: (v: string) => categoryLabel(v, t),
    },
    {
      title: t('admin.ops.ticket.col.priority'),
      dataIndex: 'priority',
      width: 100,
      render: (v: string) => priorityTag(v, t),
    },
    {
      title: t('admin.ops.ticket.col.status'),
      dataIndex: 'status',
      width: 100,
      render: (v: string) => <Tag color={ticketStatusColor(v)}>{t(`admin.ops.ticket.status.${v}`)}</Tag>,
    },
    {
      title: t('admin.ops.ticket.col.updated'),
      dataIndex: 'updated_at',
      width: 160,
      render: (v: string) => <span className="wg-cell-dim">{formatDateTime(v)}</span>,
    },
    {
      title: t('common.actions'),
      width: 160,
      render: (_: unknown, r: AdminTicketRow) => (
        <div className="wg-cell-actions">
          <Button theme="borderless" size="small" onClick={() => setDetailId(r.id)}>
            {t('common.view')}
          </Button>
          {r.status !== 'resolved' && r.status !== 'closed' && (
            <Button theme="borderless" size="small" onClick={() => changeStatus(r, 'resolved')}>
              {t('admin.ops.ticket.resolve')}
            </Button>
          )}
        </div>
      ),
    },
  ];

  const annColumns = [
    {
      title: t('admin.ops.ann.col.title'),
      dataIndex: 'title',
      render: (v: string, r: AdminAnnouncement) => (
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <span className="wg-cell-strong wg-cell-ellipsis" title={v}>
            {v || '—'}
          </span>
          {r.pinned ? <Tag color="orange">{t('admin.ops.ann.pinned')}</Tag> : null}
        </div>
      ),
    },
    {
      title: t('admin.ops.ann.col.type'),
      dataIndex: 'type',
      width: 100,
      render: (v: string) => <Tag color={v === 'warning' ? 'orange' : v === 'success' ? 'green' : 'blue'}>{t(`admin.ops.ann.type.${v ?? 'info'}`)}</Tag>,
    },
    {
      title: t('admin.ops.ann.col.status'),
      dataIndex: 'status',
      width: 100,
      render: (v: string) => (
        <Tag color={v === 'published' ? 'green' : v === 'archived' ? 'grey' : 'blue'}>
          {t(`admin.ops.ann.status.${v ?? 'draft'}`)}
        </Tag>
      ),
    },
    {
      title: t('admin.ops.ann.col.window'),
      width: 220,
      render: (_: unknown, r: AdminAnnouncement) => (
        <span className="wg-cell-dim">
          {r.start_at || r.end_at
            ? `${formatDateTime(r.start_at)} → ${formatDateTime(r.end_at)}`
            : t('admin.ops.ann.noWindow')}
        </span>
      ),
    },
    {
      title: t('admin.ops.ann.col.created'),
      dataIndex: 'created_at',
      width: 160,
      render: (v: string) => <span className="wg-cell-dim">{formatDateTime(v)}</span>,
    },
    {
      title: t('common.actions'),
      width: 220,
      render: (_: unknown, r: AdminAnnouncement) => (
        <div className="wg-cell-actions">
          <Button theme="borderless" size="small" onClick={() => openEdit(r)} loading={loadingEdit}>
            {t('common.edit')}
          </Button>
          {r.status !== 'published' ? (
            <Button
              theme="borderless"
              size="small"
              onClick={async () => {
                try {
                  await publishAdminAnnouncement(r.id);
                  Toast.success(t('admin.ops.ann.published'));
                  await loadAnnouncements();
                } catch (e) {
                  Toast.error((e as Error).message);
                }
              }}
            >
              {t('admin.ops.ann.publish')}
            </Button>
          ) : (
            <Button
              theme="borderless"
              size="small"
              onClick={async () => {
                try {
                  await archiveAdminAnnouncement(r.id);
                  Toast.success(t('admin.ops.ann.archived'));
                  await loadAnnouncements();
                } catch (e) {
                  Toast.error((e as Error).message);
                }
              }}
            >
              {t('admin.ops.ann.archive')}
            </Button>
          )}
          <Button
            theme="borderless"
            size="small"
            type="danger"
            onClick={() =>
              dangerConfirm({
                title: t('admin.ops.ann.deleteTitle'),
                content: t('admin.ops.ann.deleteDesc'),
                okText: t('common.delete'),
                cancelText: t('common.cancelAction'),
                onOk: async () => {
                  try {
                    await deleteAdminAnnouncement(r.id);
                    Toast.success(t('admin.common.deleted'));
                    await loadAnnouncements();
                  } catch (e) {
                    Toast.error((e as Error).message);
                  }
                },
              })
            }
          >
            {t('common.delete')}
          </Button>
        </div>
      ),
    },
  ];

  return (
    <div className="wg-page">
      <PageHead title={t('admin.ops.title')} />

      <Tabs type="line" activeKey={tab} onChange={(k) => setTab(k as TabKey)}>
        <Tabs.TabPane itemKey="tickets" tab={t('admin.ops.tab.tickets')}>
          <div className="wg-stat-grid">
            <StatCard label={t('admin.ops.ticket.kpi.open')} value={formatNumber(stats.open ?? 0)} />
            <StatCard label={t('admin.ops.ticket.kpi.pending')} value={formatNumber(stats.pending ?? 0)} />
            <StatCard label={t('admin.ops.ticket.kpi.resolved')} value={formatNumber(stats.resolved ?? 0)} />
            <StatCard label={t('admin.ops.ticket.kpi.needsReply')} value={formatNumber(stats.needs_reply ?? 0)} />
          </div>

          <div className="wg-pro-card wg-filter-card">
            <Select
              style={{ width: 140 }}
              value={priority}
              onChange={(v) => setPriority((v as string) ?? 'all')}
              optionList={[
                { value: 'all', label: t('admin.ops.ticket.priority.all') },
                ...PRIORITIES.map((p) => ({ value: p, label: t(`admin.ops.ticket.priority.${p}`) })),
              ]}
            />
            <Select
              style={{ width: 140 }}
              value={status}
              onChange={(v) => setStatus((v as string) ?? 'all')}
              optionList={[
                { value: 'all', label: t('admin.ops.ticket.status.all') },
                ...TICKET_STATUSES.map((s) => ({ value: s, label: t(`admin.ops.ticket.status.${s}`) })),
              ]}
            />
            <Input
              style={{ width: 240 }}
              placeholder={t('admin.ops.ticket.searchPh')}
              value={search}
              onChange={setSearch}
              prefix={<IconSearch />}
              showClear
            />
            <div className="wg-filter-actions">
              <Button type="primary" theme="solid" icon={<IconSearch />} onClick={() => loadTickets()}>
                {t('common.search')}
              </Button>
              <Button
                icon={<IconRefresh />}
                theme="light"
                onClick={() => {
                  setPriority('all');
                  setStatus('all');
                  setSearch('');
                  loadTickets('all', 'all', '');
                }}
              >
                {t('common.reset')}
              </Button>
            </div>
          </div>

          <div className="wg-pro-card wg-table-card">
            <div className="wg-table-toolbar">
              <div className="wg-table-toolbar-left">
                <span className="wg-pro-card-title">{t('admin.ops.ticket.tableTitle')}</span>
              </div>
              <Button icon={<IconRefresh />} theme="light" onClick={() => loadTickets()}>
                {t('common.refresh')}
              </Button>
            </div>
            <Table
              loading={ticketsLoading}
              rowKey="id"
              columns={ticketColumns}
              dataSource={tickets ?? []}
              pagination={{ pageSize: 10, showTotal: true }}
              tableLayout="fixed"
              empty={<Empty description={t('admin.ops.ticket.empty')} />}
            />
          </div>
        </Tabs.TabPane>

        <Tabs.TabPane itemKey="announcements" tab={t('admin.ops.tab.announcements')}>
          <div className="wg-pro-card wg-table-card">
            <div className="wg-table-toolbar">
              <div className="wg-table-toolbar-left">
                <span className="wg-pro-card-title">
                  {t('admin.ops.ann.tableTitle')}
                  <span className="wg-cell-dim"> · {formatNumber(annTotal)}</span>
                </span>
              </div>
              <div style={{ display: 'flex', gap: 8 }}>
                <Button icon={<IconRefresh />} theme="light" onClick={() => loadAnnouncements()}>
                  {t('common.refresh')}
                </Button>
                <Button icon={<IconPlus />} type="primary" theme="solid" onClick={() => setCreateOpen(true)}>
                  {t('admin.ops.ann.create')}
                </Button>
              </div>
            </div>
            <Table
              loading={annLoading}
              rowKey="id"
              columns={annColumns}
              dataSource={announcements ?? []}
              pagination={{
                currentPage: annPage,
                pageSize: 20,
                total: annTotal,
                onPageChange: (p) => loadAnnouncements(p),
              }}
              tableLayout="fixed"
              empty={<Empty description={t('admin.ops.ann.empty')} />}
            />
            <span className="wg-cell-dim">
              {t('admin.ops.ann.pageHint', { pages: annTotalPages })}
            </span>
          </div>
        </Tabs.TabPane>
      </Tabs>

      {detailId && (
        <TicketDetailDrawer
          id={detailId}
          onClose={() => setDetailId(null)}
          onChanged={() => loadTickets()}
        />
      )}

      {createOpen && (
        <AnnouncementDrawer
          onClose={() => setCreateOpen(false)}
          onSaved={() => loadAnnouncements(1)}
        />
      )}

      {editTarget && (
        <AnnouncementDrawer
          target={editTarget}
          onClose={() => setEditTarget(null)}
          onSaved={() => loadAnnouncements()}
        />
      )}
    </div>
  );
}

function StatCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="wg-pro-card wg-stat">
      <div className="wg-stat-body">
        <div className="wg-stat-label">{label}</div>
        <div className="wg-stat-value">{value}</div>
      </div>
    </div>
  );
}

function categoryLabel(v: string, t: (k: string) => string) {
  const key = `admin.ops.ticket.category.${v}`;
  const label = t(key);
  return label === key ? v || '—' : label;
}

function priorityTag(v: string, t: (k: string) => string) {
  const color = v === 'urgent' ? 'red' : v === 'high' ? 'orange' : v === 'low' ? 'grey' : 'blue';
  return <Tag color={color}>{t(`admin.ops.ticket.priority.${v ?? 'normal'}`)}</Tag>;
}

function ticketStatusColor(v: string) {
  switch (v) {
    case 'open':
      return 'blue';
    case 'pending':
      return 'orange';
    case 'answered':
      return 'violet';
    case 'resolved':
      return 'green';
    default:
      return 'grey';
  }
}

function TicketDetailDrawer({
  id,
  onClose,
  onChanged,
}: {
  id: string;
  onClose: () => void;
  onChanged: () => void;
}) {
  const { t } = useI18n();
  const [detail, setDetail] = useState<AdminTicketDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [body, setBody] = useState('');
  const [internal, setInternal] = useState(false);
  const [sending, setSending] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setDetail(await getAdminTicket(id));
    } catch (e) {
      Toast.error((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, [id]);

  useEffect(() => {
    load();
  }, [load]);

  const send = async () => {
    if (!body.trim()) {
      Toast.error(t('admin.ops.ticket.replyRequired'));
      return;
    }
    setSending(true);
    try {
      await replyAdminTicket(id, body.trim(), internal);
      Toast.success(t('admin.ops.ticket.replyDone'));
      setBody('');
      await Promise.all([load(), Promise.resolve(onChanged())]);
    } catch (e) {
      Toast.error((e as Error).message);
    } finally {
      setSending(false);
    }
  };

  const setStatus = async (next: string) => {
    try {
      await patchAdminTicket(id, { status: next });
      Toast.success(t(`admin.ops.ticket.${next === 'closed' ? 'closed' : 'resolved'}Done`));
      await load();
      onChanged();
    } catch (e) {
      Toast.error((e as Error).message);
    }
  };

  const ticket = detail?.ticket;

  return (
    <SideSheet
      title={
        ticket
          ? `#${ticket.ticket_no} · ${ticket.subject}`
          : t('admin.ops.ticket.detailTitle')
      }
      visible
      onCancel={onClose}
      width={760}
      closeOnEsc
      maskClosable={false}
      footer={
        <div className="wg-drawer-footer">
          <Button onClick={onClose}>{t('common.close')}</Button>
          {ticket && ticket.status !== 'resolved' && ticket.status !== 'closed' && (
            <Button theme="light" onClick={() => setStatus('resolved')}>
              {t('admin.ops.ticket.resolve')}
            </Button>
          )}
          {ticket && ticket.status !== 'closed' && (
            <Button type="danger" theme="solid" onClick={() => setStatus('closed')}>
              {t('admin.ops.ticket.close')}
            </Button>
          )}
        </div>
      }
    >
      {loading ? (
        <div className="wg-page-loading">
          <span className="wg-text-2">{t('common.loading')}</span>
        </div>
      ) : !detail ? (
        <Empty description={t('admin.ops.ticket.empty')} />
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          <div className="wg-pro-card" style={{ padding: 16 }}>
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(160px, 1fr))', gap: 12 }}>
              <Meta label={t('admin.ops.ticket.meta.requester')} value={detail.ticket.requester_email || '—'} />
              <Meta label={t('admin.ops.ticket.meta.category')} value={categoryLabel(detail.ticket.category, t)} />
              <Meta
                label={t('admin.ops.ticket.meta.priority')}
                value={t(`admin.ops.ticket.priority.${detail.ticket.priority ?? 'normal'}`)}
              />
              <Meta
                label={t('admin.ops.ticket.meta.status')}
                value={t(`admin.ops.ticket.status.${detail.ticket.status ?? 'open'}`)}
              />
              <Meta label={t('admin.ops.ticket.meta.created')} value={formatDateTime(detail.ticket.created_at)} />
              <Meta label={t('admin.ops.ticket.meta.assignee')} value={detail.ticket.assignee_email || '—'} />
            </div>
          </div>

          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            <span className="wg-pro-card-title">{t('admin.ops.ticket.thread')}</span>
            {detail.messages.length === 0 ? (
              <Empty description={t('admin.ops.ticket.noMessages')} />
            ) : (
              detail.messages.map((m) => (
                <div
                  key={m.id}
                  className="wg-pro-card"
                  style={{
                    padding: 14,
                    borderLeft:
                      m.author_role === 'admin'
                        ? '3px solid var(--semi-color-primary)'
                        : '3px solid var(--semi-color-border)',
                    background: m.internal_note ? 'var(--wg-pill-bg)' : undefined,
                  }}
                >
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
                    <Tag color={m.author_role === 'admin' ? 'blue' : 'grey'}>
                      {m.author_role === 'admin'
                        ? t('admin.ops.ticket.authorAdmin')
                        : t('admin.ops.ticket.authorUser')}
                    </Tag>
                    {m.internal_note && <Tag color="orange">{t('admin.ops.ticket.internalNoteTag')}</Tag>}
                    <span className="wg-cell-dim">{m.author_email || '—'}</span>
                    <span className="wg-cell-dim">{formatDateTime(m.created_at)}</span>
                  </div>
                  <div style={{ whiteSpace: 'pre-wrap', lineHeight: 1.6 }}>{m.body}</div>
                </div>
              ))
            )}
          </div>

          <div className="wg-form-field">
            <label className="wg-form-label">{t('admin.ops.ticket.reply')}</label>
            <TextArea
              rows={4}
              value={body}
              onChange={setBody}
              placeholder={t('admin.ops.ticket.replyPh')}
              maxLength={10000}
            />
            <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
              <Switch
                size="small"
                checked={internal}
                onChange={setInternal}
                aria-label={t('admin.ops.ticket.internalNote')}
              />
              <span className="wg-cell-dim">{t('admin.ops.ticket.internalNote')}</span>
              <Button
                type="primary"
                theme="solid"
                loading={sending}
                onClick={send}
                style={{ marginLeft: 'auto' }}
              >
                {t('admin.ops.ticket.sendReply')}
              </Button>
            </div>
          </div>
        </div>
      )}
    </SideSheet>
  );
}

function Meta({ label, value }: { label: string; value: string }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
      <span className="wg-cell-dim">{label}</span>
      <span className="wg-cell-strong">{value}</span>
    </div>
  );
}

function AnnouncementDrawer({
  target,
  onClose,
  onSaved,
}: {
  target?: AdminAnnouncementDetail;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useI18n();
  const editing = Boolean(target);
  const [title, setTitle] = useState(target?.title ?? '');
  const [content, setContent] = useState(target?.content ?? '');
  const [type, setType] = useState(target?.type ?? 'info');
  const [pinned, setPinned] = useState(Boolean(target?.pinned));
  const [startAt, setStartAt] = useState(toDateInput(target?.start_at));
  const [endAt, setEndAt] = useState(toDateInput(target?.end_at));
  const [saving, setSaving] = useState(false);

  const save = async () => {
    if (!title.trim()) {
      Toast.error(t('admin.ops.ann.titleRequired'));
      return;
    }
    if (!content.trim()) {
      Toast.error(t('admin.ops.ann.contentRequired'));
      return;
    }
    const payload: AnnouncementPayload = {
      title: title.trim(),
      content: content.trim(),
      type,
      pinned,
      start_at: startAt || undefined,
      end_at: endAt || undefined,
    };
    setSaving(true);
    try {
      if (editing && target) {
        await updateAdminAnnouncement(target.id, payload);
        Toast.success(t('admin.common.saved'));
      } else {
        await createAdminAnnouncement(payload);
        Toast.success(t('admin.common.created'));
      }
      onSaved();
      onClose();
    } catch (e) {
      Toast.error((e as Error).message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <SideSheet
      title={t(editing ? 'admin.ops.ann.editTitle' : 'admin.ops.ann.createTitle')}
      visible
      onCancel={onClose}
      width={680}
      closeOnEsc
      maskClosable={false}
      footer={
        <div className="wg-drawer-footer">
          <Button onClick={onClose}>{t('common.cancel')}</Button>
          <Button type="primary" theme="solid" loading={saving} onClick={save}>
            {t('common.save')}
          </Button>
        </div>
      }
    >
      <div className="wg-field-grid">
        <div className="wg-form-field">
          <label className="wg-form-label">
            {t('admin.ops.ann.field.title')}
            <span className="wg-form-required">*</span>
          </label>
          <Input value={title} onChange={setTitle} placeholder={t('admin.ops.ann.titlePh')} />
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">
            {t('admin.ops.ann.field.content')}
            <span className="wg-form-required">*</span>
          </label>
          <TextArea
            rows={6}
            value={content}
            onChange={setContent}
            placeholder={t('admin.ops.ann.contentPh')}
          />
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.ops.ann.field.type')}</label>
          <Select
            value={type}
            onChange={(v) => setType((v as string) ?? 'info')}
            optionList={ANNOUNCEMENT_TYPES.map((v) => ({
              value: v,
              label: t(`admin.ops.ann.type.${v}`),
            }))}
            style={{ width: 220 }}
          />
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.ops.ann.field.pinned')}</label>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            <Switch
              checked={pinned}
              onChange={setPinned}
              aria-label={t('admin.ops.ann.field.pinned')}
            />
            <span className="wg-cell-dim">{t('admin.ops.ann.pinnedHint')}</span>
          </div>
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.ops.ann.field.window')}</label>
          <div style={{ display: 'flex', gap: 12, alignItems: 'center' }}>
            <Input type="date" value={startAt} onChange={setStartAt} style={{ width: 180 }} />
            <span className="wg-cell-dim">→</span>
            <Input type="date" value={endAt} onChange={setEndAt} style={{ width: 180 }} />
          </div>
          <span className="wg-cell-dim">{t('admin.ops.ann.windowHint')}</span>
        </div>
      </div>
    </SideSheet>
  );
}

function toDateInput(v: string | null | undefined): string {
  if (!v) return '';
  const d = new Date(v);
  if (Number.isNaN(d.getTime())) return v.slice(0, 10);
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}
