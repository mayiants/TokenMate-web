import { request } from './client';

/**
 * System-admin operations surface (P3-A3).
 *
 * Sources:
 *  - tickets:       internal/routes/tickets.go (admin group /api/admin/tickets)
 *  - announcements: internal/routes/announcements.go (/api/admin/announcements)
 */

// --- support tickets --------------------------------------------------------

export interface AdminTicketRow {
  id: string;
  ticket_no: number;
  subject: string;
  category: string;
  priority: string;
  status: string;
  last_reply_role?: string;
  last_reply_at?: string | null;
  created_at?: string;
  updated_at?: string;
  requester_email?: string;
  assignee_email?: string;
  enterprise_id?: string;
  assignee_id?: string;
}

export interface AdminTicketMessage {
  id: string;
  author_role: string;
  body: string;
  internal_note?: boolean;
  created_at?: string;
  author_email?: string;
  attachments?: { id: string; filename: string; size_bytes?: number; mime_type?: string }[];
}

export interface AdminTicketDetail {
  ticket: AdminTicketRow;
  messages: AdminTicketMessage[];
}

export async function listAdminTicketRows(params: {
  status?: string;
  category?: string;
  priority?: string;
  search?: string;
} = {}): Promise<AdminTicketRow[]> {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v) q.set(k, v);
  }
  const qs = q.toString();
  const res = await request<{ tickets?: AdminTicketRow[] }>(
    `/api/admin/tickets${qs ? `?${qs}` : ''}`
  );
  return res.tickets ?? [];
}

export function getAdminTicket(id: string): Promise<AdminTicketDetail> {
  return request<AdminTicketDetail>(`/api/admin/tickets/${id}`);
}

/** Internal notes stay admin-only and do not change ticket state. */
export async function replyAdminTicket(
  id: string,
  body: string,
  internalNote = false
): Promise<void> {
  await request(`/api/admin/tickets/${id}/messages`, {
    method: 'POST',
    body: { body, internal_note: internalNote },
  });
}

export async function patchAdminTicket(
  id: string,
  payload: { status?: string; priority?: string; category?: string }
): Promise<void> {
  await request(`/api/admin/tickets/${id}`, { method: 'PATCH', body: payload });
}

// --- announcements ----------------------------------------------------------

export interface AdminAnnouncement {
  id: string;
  title: string;
  type?: string;
  status?: string;
  pinned?: boolean;
  priority?: number;
  start_at?: string | null;
  end_at?: string | null;
  created_by?: string | null;
  created_at?: string;
  updated_at?: string;
}

export interface AdminAnnouncementPage {
  announcements: AdminAnnouncement[];
  total: number;
  page: number;
  limit: number;
  total_pages: number;
}

export async function listAdminAnnouncements(
  page = 1,
  limit = 20
): Promise<AdminAnnouncementPage> {
  const res = await request<AdminAnnouncementPage>(
    `/api/admin/announcements?page=${page}&limit=${limit}`
  );
  return {
    announcements: res.announcements ?? [],
    total: res.total ?? 0,
    page: res.page ?? page,
    limit: res.limit ?? limit,
    total_pages: res.total_pages ?? 1,
  };
}

export interface AnnouncementPayload {
  title: string;
  content: string;
  /** info | warning | success */
  type?: string;
  pinned?: boolean;
  /** YYYY-MM-DD */
  start_at?: string;
  /** YYYY-MM-DD */
  end_at?: string;
}

/** Creates the announcement as a draft; publish it separately. */
export async function createAdminAnnouncement(payload: AnnouncementPayload): Promise<string> {
  const res = await request<{ id?: string }>('/api/admin/announcements', {
    method: 'POST',
    body: payload,
  });
  return res.id ?? '';
}

export async function updateAdminAnnouncement(
  id: string,
  payload: Partial<AnnouncementPayload>
): Promise<void> {
  await request(`/api/admin/announcements/${id}`, { method: 'PUT', body: payload });
}

export async function publishAdminAnnouncement(id: string): Promise<void> {
  await request(`/api/admin/announcements/${id}/publish`, { method: 'POST' });
}

/** "下架" = archive; the row stays visible with status archived. */
export async function archiveAdminAnnouncement(id: string): Promise<void> {
  await request(`/api/admin/announcements/${id}/archive`, { method: 'POST' });
}

export async function deleteAdminAnnouncement(id: string): Promise<void> {
  await request(`/api/admin/announcements/${id}`, { method: 'DELETE' });
}

/** Detail (content is not in the list payload); used by the edit drawer. */
export interface AdminAnnouncementDetail extends AdminAnnouncement {
  content?: string;
  read_count?: number;
}

export function getAdminAnnouncement(id: string): Promise<AdminAnnouncementDetail> {
  return request<AdminAnnouncementDetail>(`/api/admin/announcements/${id}`);
}
