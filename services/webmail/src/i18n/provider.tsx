"use client";

import React, { createContext, useCallback, useContext, useMemo, useState } from "react";
import {
  LOCALE_COOKIE,
  MESSAGES,
  DEFAULT_LOCALE,
  translate,
  type Locale,
  type TranslateParams,
} from "./config";

export type TranslateFn = (key: string, params?: TranslateParams) => string;

interface I18nContextValue {
  locale: Locale;
  setLocale: (locale: Locale) => void;
  t: TranslateFn;
}

const I18nContext = createContext<I18nContextValue | null>(null);

export function I18nProvider({ locale: initialLocale, children }: { locale: Locale; children: React.ReactNode }) {
  const [locale, setLocaleState] = useState<Locale>(initialLocale);

  const setLocale = useCallback((next: Locale) => {
    setLocaleState(next);
    // Persisted for the next server render, so the page does not come back in
    // the previous language on reload. Not HttpOnly: this is a display
    // preference the client itself sets, carrying nothing sensitive.
    document.cookie = `${LOCALE_COOKIE}=${next}; Path=/; Max-Age=31536000; SameSite=Lax`;
    // Best effort: keep the account's stored locale in step for anything the
    // server sends by mail. A failure here must not break the UI switch.
    void fetch("/api/v1/user/locale", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ locale: next }),
    }).catch(() => undefined);
    document.documentElement.lang = next;
  }, []);

  const value = useMemo<I18nContextValue>(() => {
    const messages = MESSAGES[locale] ?? MESSAGES[DEFAULT_LOCALE];
    return { locale, setLocale, t: (key, params) => translate(messages, key, params) };
  }, [locale, setLocale]);

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n(): I18nContextValue {
  const ctx = useContext(I18nContext);
  if (!ctx) {
    throw new Error("useI18n must be used inside <I18nProvider>");
  }
  return ctx;
}

/** Shorthand for components that only need to translate. */
export function useTranslations(): TranslateFn {
  return useI18n().t;
}
