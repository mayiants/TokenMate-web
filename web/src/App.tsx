import { Routes, Route, Outlet } from 'react-router-dom';
import PublicLayout from './layouts/PublicLayout';
import ConsoleLayout from './layouts/ConsoleLayout';
import RequireAuth from './components/RequireAuth';
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
const EntLayout = lazy(() => import('./pages/enterprise/EntLayout'));
const EntOverview = lazy(() => import('./pages/enterprise/Overview'));
const EntMembers = lazy(() => import('./pages/enterprise/Members'));
const EntGroups = lazy(() => import('./pages/enterprise/Groups'));
const EntKeys = lazy(() => import('./pages/enterprise/Keys'));
const EntUsage = lazy(() => import('./pages/enterprise/Usage'));
const EntBilling = lazy(() => import('./pages/enterprise/Billing'));
const EntSettings = lazy(() => import('./pages/enterprise/Settings'));

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
        <Route
          path="enterprise"
          element={
            <Suspense fallback={lazyFallback}>
              <EntLayout>
                <Outlet />
              </EntLayout>
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
