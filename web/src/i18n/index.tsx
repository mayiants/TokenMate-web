import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from 'react';
import zh from './locales/zh.json';
import en from './locales/en.json';

export type Locale = 'zh' | 'en';

const dicts = { zh, en } as const;

interface I18nState {
  locale: Locale;
  setLocale: (l: Locale) => void;
  t: (key: string, params?: Record<string, string | number>) => string;
}

const I18nContext = createContext<I18nState | null>(null);

function load(): Locale {
  const v = localStorage.getItem('wg-locale');
  return v === 'en' ? 'en' : 'zh';
}

export function I18nProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<Locale>(load);

  const setLocale = useCallback((l: Locale) => {
    localStorage.setItem('wg-locale', l);
    setLocaleState(l);
  }, []);

  const t = useCallback(
    (key: string, params?: Record<string, string | number>) => {
      const dict = dicts[locale] as Record<string, string>;
      let text = dict[key] ?? key;
      if (params) {
        for (const [k, v] of Object.entries(params)) {
          text = text.split(`{${k}}`).join(String(v));
        }
      }
      return text;
    },
    [locale]
  );

  const value = useMemo(() => ({ locale, setLocale, t }), [locale, setLocale, t]);
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n() {
  const ctx = useContext(I18nContext);
  if (!ctx) throw new Error('useI18n must be used within I18nProvider');
  return ctx;
}
