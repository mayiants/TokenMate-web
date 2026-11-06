import { request } from './client';

/**
 * Per-user inbox (internal/routes/notifications.go).
 * GET /api/notifications is paginated server-side at a fixed 20/page;
 * type / read filters are applied client-side by the page.
 */
export interface NotificationItem {
  id: string;
  type: string;
  scope?: string;
  priority?: string;
  title: string;
  body: string;
  link?: string;
  read_at?: string | null;
  created_at: string;
}

export interface NotificationPage {
  items: NotificationItem[];
  unread: number;
  page: number;
  per_page: number;
}

export async function listNotifications(page = 1): Promise<NotificationPage> {
  return request<NotificationPage>(`/api/notifications?page=${page}`);
}

export async function markNotificationRead(id: string): Promise<void> {
  await request<{ ok: boolean }>(`/api/notifications/${id}/read`, { method: 'POST' });
}

export async function markAllNotificationsRead(): Promise<void> {
  await request<{ ok: boolean }>('/api/notifications/read-all', { method: 'POST' });
}
