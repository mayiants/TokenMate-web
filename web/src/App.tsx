import { Routes, Route, Outlet } from 'react-router-dom';
import PublicLayout from './layouts/PublicLayout';
import ConsoleLayout from './layouts/ConsoleLayout';
import RequireAuth from './components/RequireAuth';
import { RequireAdminRole, RequireEnterpriseMember } from './components/RequireRole';
import Landing from './pages/Landing';
import ModelsGallery from './pages/ModelsGallery';
import Login from './pages/auth/Login';
import ForgotPassword from './pages/auth/ForgotPassword';
import Register from './pages/auth/Register';
import { lazy, Suspense } from 'react';
import NotFound from './pages/NotFound';

// Route-level code splitting: the console/enterprise chunks (with echarts)
// load on demand, keeping the public bundle lean.
const Dashboard = lazy(() => import('./pages/console/Dashboard'));
const ApiKeys = lazy(() => import('./pages/console/ApiKeys'));
const UsageLogs = lazy(() => import('./pages/console/UsageLogs'));
const BalancePage = lazy(() => import('./pages/console/Balance'));
const ProfilePage = lazy(() => import('./pages/console/Profile'));
const NotificationsPage = lazy(() => import('./pages/console/Notifications'));
const SupportPage = lazy(() => import('./pages/console/Support'));
const BillingPage = lazy(() => import('./pages/console/Billing'));
const SubscriptionsPage = lazy(() => import('./pages/console/Subscriptions'));
const GroupsPage = lazy(() => import('./pages/console/Groups'));
const ServiceStatusPage = lazy(() => import('./pages/console/ServiceStatus'));
const EntLayout = lazy(() => import('./pages/enterprise/EntLayout'));
const EntOverview = lazy(() => import('./pages/enterprise/Overview'));
const EntMembers = lazy(() => import('./pages/enterprise/Members'));
const EntGroups = lazy(() => import('./pages/enterprise/Groups'));
const EntKeys = lazy(() => import('./pages/enterprise/Keys'));
const EntUsage = lazy(() => import('./pages/enterprise/Usage'));
const EntBilling = lazy(() => import('./pages/enterprise/Billing'));
const EntSettings = lazy(() => import('./pages/enterprise/Settings'));
const EntAudit = lazy(() => import('./pages/enterprise/Audit'));
const EntAlerts = lazy(() => import('./pages/enterprise/Alerts'));
const AdminLayout = lazy(() => import('./pages/admin/AdminLayout'));
const AdminOverview = lazy(() => import('./pages/admin/Overview'));
const AdminUsers = lazy(() => import('./pages/admin/Users'));
const AdminEnterprises = lazy(() => import('./pages/admin/Enterprises'));
const AdminGroups = lazy(() => import('./pages/admin/Groups'));
const AdminModelsAdmin = lazy(() => import('./pages/admin/ModelsAdmin'));
const AdminUpstream = lazy(() => import('./pages/admin/Upstream'));
const AdminFinance = lazy(() => import('./pages/admin/Finance'));
const AdminOps = lazy(() => import('./pages/admin/Ops'));
const AdminSecurity = lazy(() => import('./pages/admin/Security'));
const AdminSettings = lazy(() => import('./pages/admin/Settings'));

const lazyFallback = (
  <div style={{ display: 'flex', justifyContent: 'center', padding: 96 }}>
    <span className="wg-text-2">Loading…</span>
  </div>
);

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/register" element={<Register />} />
      <Route path="/forgot-password" element={<ForgotPassword />} />
      <Route
        path="/console"
        element={
          <RequireAuth>
            <ConsoleLayout />
          </RequireAuth>
        }
      >
        <Route index element={<Suspense fallback={lazyFallback}><Dashboard /></Suspense>} />
        <Route path="keys" element={<Suspense fallback={lazyFallback}><ApiKeys /></Suspense>} />
        <Route path="logs" element={<Suspense fallback={lazyFallback}><UsageLogs /></Suspense>} />
        <Route path="balance" element={<Suspense fallback={lazyFallback}><BalancePage /></Suspense>} />
        <Route path="profile" element={<Suspense fallback={lazyFallback}><ProfilePage /></Suspense>} />
        <Route path="notifications" element={<Suspense fallback={lazyFallback}><NotificationsPage /></Suspense>} />
        <Route path="support" element={<Suspense fallback={lazyFallback}><SupportPage /></Suspense>} />
        <Route path="billing" element={<Suspense fallback={lazyFallback}><BillingPage /></Suspense>} />
        <Route path="subscriptions" element={<Suspense fallback={lazyFallback}><SubscriptionsPage /></Suspense>} />
        <Route path="groups" element={<Suspense fallback={lazyFallback}><GroupsPage /></Suspense>} />
        <Route path="status" element={<Suspense fallback={lazyFallback}><ServiceStatusPage /></Suspense>} />
        <Route
          path="enterprise"
          element={
            <Suspense fallback={lazyFallback}>
              <RequireEnterpriseMember>
                <EntLayout>
                  <Outlet />
                </EntLayout>
              </RequireEnterpriseMember>
            </Suspense>
          }
        >
          <Route index element={<Suspense fallback={lazyFallback}><EntOverview /></Suspense>} />
          <Route path="members" element={<Suspense fallback={lazyFallback}><EntMembers /></Suspense>} />
          <Route path="groups" element={<Suspense fallback={lazyFallback}><EntGroups /></Suspense>} />
          <Route path="keys" element={<Suspense fallback={lazyFallback}><EntKeys /></Suspense>} />
          <Route path="usage" element={<Suspense fallback={lazyFallback}><EntUsage /></Suspense>} />
          <Route path="billing" element={<Suspense fallback={lazyFallback}><EntBilling /></Suspense>} />
          <Route path="settings" element={<Suspense fallback={lazyFallback}><EntSettings /></Suspense>} />
          <Route path="audit" element={<Suspense fallback={lazyFallback}><EntAudit /></Suspense>} />
          <Route path="alerts" element={<Suspense fallback={lazyFallback}><EntAlerts /></Suspense>} />
        </Route>
        <Route
          path="admin"
          element={
            <Suspense fallback={lazyFallback}>
              <RequireAdminRole>
                <AdminLayout />
              </RequireAdminRole>
            </Suspense>
          }
        >
          <Route index element={<Suspense fallback={lazyFallback}><AdminOverview /></Suspense>} />
          <Route path="users" element={<Suspense fallback={lazyFallback}><AdminUsers /></Suspense>} />
          <Route path="enterprises" element={<Suspense fallback={lazyFallback}><AdminEnterprises /></Suspense>} />
          <Route path="groups" element={<Suspense fallback={lazyFallback}><AdminGroups /></Suspense>} />
          <Route path="models" element={<Suspense fallback={lazyFallback}><AdminModelsAdmin /></Suspense>} />
          <Route path="upstream" element={<Suspense fallback={lazyFallback}><AdminUpstream /></Suspense>} />
          <Route path="finance" element={<Suspense fallback={lazyFallback}><AdminFinance /></Suspense>} />
          <Route path="ops" element={<Suspense fallback={lazyFallback}><AdminOps /></Suspense>} />
          <Route path="security" element={<Suspense fallback={lazyFallback}><AdminSecurity /></Suspense>} />
          <Route path="settings" element={<Suspense fallback={lazyFallback}><AdminSettings /></Suspense>} />
        </Route>
      </Route>
      <Route element={<PublicLayout />}>
        <Route path="/" element={<Landing />} />
        <Route path="/models" element={<ModelsGallery />} />
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  );
}
