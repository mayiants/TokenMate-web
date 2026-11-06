import React from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { ConfigProvider } from '@douyinfe/semi-ui';
import '@fontsource-variable/inter';
import App from './App';
import { I18nProvider, useI18n } from './i18n';
import { AuthProvider } from './auth/AuthContext';
import { applyThemeMode, ThemeProvider } from './utils/theme';

// Semi UI base styles + zh_CN locale (localized, no CDN).
// The prebuilt stylesheet is not in semi-ui's exports map, so import it by
// relative path (Vite resolves it as a plain file).
import '../node_modules/@douyinfe/semi-ui/dist/css/semi.min.css';
import './styles/global.css';
import zh_CN from '@douyinfe/semi-ui/lib/es/locale/source/zh_CN';
import en_US from '@douyinfe/semi-ui/lib/es/locale/source/en_US';

// Apply persisted/system theme before first paint to avoid a light flash.
applyThemeMode(
  localStorage.getItem('wg-theme') === 'dark' ||
    (!localStorage.getItem('wg-theme') && window.matchMedia?.('(prefers-color-scheme: dark)').matches)
    ? 'dark'
    : 'light'
);

function Root() {
  const { locale } = useI18n();
  return (
    <ConfigProvider locale={locale === 'zh' ? zh_CN : en_US}>
      <App />
    </ConfigProvider>
  );
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter>
      <I18nProvider>
        <ThemeProvider>
          <AuthProvider>
            <Root />
          </AuthProvider>
        </ThemeProvider>
      </I18nProvider>
    </BrowserRouter>
  </React.StrictMode>
);
