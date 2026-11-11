// Single source of truth for the routes the UI gates cover.
// Every gate (axe / console-error / responsive) imports this list so a new page
// cannot silently escape CI. Previously each scanner kept its own copy, which
// drifted (the admin block was duplicated, enterprise sub-pages were missing).
const PUBLIC_PAGES = ['/', '/models', '/login', '/register', '/forgot-password'];

// Signed-in user console (no platform-admin rights needed).
const CONSOLE_PAGES = [
  '/console',
  '/console/keys',
  '/console/logs',
  '/console/balance',
  '/console/profile',
  '/console/notifications',
  '/console/support',
  '/console/billing',
  '/console/subscriptions',
  '/console/groups',
  '/console/status',
  '/console/enterprise',
  '/console/enterprise/members',
  '/console/enterprise/groups',
  '/console/enterprise/keys',
  '/console/enterprise/usage',
  '/console/enterprise/billing',
  '/console/enterprise/settings',
  '/console/enterprise/audit',
  '/console/enterprise/alerts',
];

// Platform administration (system_admin / super_admin only).
const ADMIN_PAGES = [
  '/console/admin',
  '/console/admin/users',
  '/console/admin/enterprises',
  '/console/admin/groups',
  '/console/admin/models',
  '/console/admin/upstream',
  '/console/admin/usage-explorer',
  '/console/admin/rate-limits',
  '/console/admin/finance',
  '/console/admin/ops',
  '/console/admin/security',
  '/console/admin/settings',
];

module.exports = { PUBLIC_PAGES, CONSOLE_PAGES, ADMIN_PAGES };
