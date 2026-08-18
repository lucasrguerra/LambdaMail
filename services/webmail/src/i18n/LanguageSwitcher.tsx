"use client";

import { LOCALES, LOCALE_LABEL_KEYS, type Locale } from "./config";
import { useI18n } from "./provider";

/** Language selector, rendered in both surface layouts (PLAN.md section 21.2). */
export function LanguageSwitcher({ className = "" }: { className?: string }) {
  const { locale, setLocale, t } = useI18n();

  return (
    <label className={`flex min-w-0 flex-1 items-center gap-2 text-xs ${className}`}>
      <span className="sr-only">{t("settings.language")}</span>
      <select
        aria-label={t("settings.language")}
        value={locale}
        onChange={(e) => setLocale(e.target.value as Locale)}
        className="min-h-[32px] w-full rounded-[9px] bg-dark-panel px-2 py-1 text-xs text-slate-100 shadow-edge focus-visible:shadow-edge-accent"
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
