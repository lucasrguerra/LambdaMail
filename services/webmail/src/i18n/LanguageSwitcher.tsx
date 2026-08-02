"use client";

import { LOCALES, LOCALE_LABEL_KEYS, type Locale } from "./config";
import { useI18n } from "./provider";

/** Language selector, rendered in both surface layouts (PLAN.md section 21.2). */
export function LanguageSwitcher({ className = "" }: { className?: string }) {
  const { locale, setLocale, t } = useI18n();

  return (
    <label className={`flex items-center gap-2 text-xs ${className}`}>
      <span className="sr-only">{t("settings.language")}</span>
      <select
        aria-label={t("settings.language")}
        value={locale}
        onChange={(e) => setLocale(e.target.value as Locale)}
        className="rounded-md border border-slate-700 bg-slate-900 px-2 py-1 text-xs text-slate-200"
      >
        {LOCALES.map((l) => (
          <option key={l} value={l}>
            {t(LOCALE_LABEL_KEYS[l])}
          </option>
        ))}
      </select>
    </label>
  );
}
