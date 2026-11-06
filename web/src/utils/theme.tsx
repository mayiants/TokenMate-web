import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from 'react';

export type ThemeMode = 'light' | 'dark';

const STORAGE_KEY = 'wg-theme';
const DARK_QUERY = '(prefers-color-scheme: dark)';

function load(): ThemeMode {
  const v = localStorage.getItem(STORAGE_KEY);
  if (v === 'dark' || v === 'light') return v;
  return window.matchMedia?.(DARK_QUERY).matches ? 'dark' : 'light';
}

/** Semi dark mode triggers on body[theme-mode='dark']; .theme-dark drives our --wg-* vars. */
export function applyThemeMode(mode: ThemeMode) {
  document.body.classList.toggle('theme-dark', mode === 'dark');
  if (mode === 'dark') document.body.setAttribute('theme-mode', 'dark');
  else document.body.removeAttribute('theme-mode');
}

interface ThemeState {
  mode: ThemeMode;
  toggle: () => void;
}

const ThemeContext = createContext<ThemeState | null>(null);

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [mode, setMode] = useState<ThemeMode>(load);

  const toggle = useCallback(() => {
    setMode((prev) => {
      const next: ThemeMode = prev === 'dark' ? 'light' : 'dark';
      localStorage.setItem(STORAGE_KEY, next);
      applyThemeMode(next);
      return next;
    });
  }, []);

  const value = useMemo(() => ({ mode, toggle }), [mode, toggle]);
  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

export function useTheme() {
  const ctx = useContext(ThemeContext);
  if (!ctx) throw new Error('useTheme must be used within ThemeProvider');
  return ctx;
}
