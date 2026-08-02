import en from "../../messages/en.json";
import ptBR from "../../messages/pt-BR.json";
import es from "../../messages/es.json";

export const LOCALES = ["en", "pt-BR", "es"] as const;
export type Locale = (typeof LOCALES)[number];

export const DEFAULT_LOCALE: Locale = "en";
export const LOCALE_COOKIE = "lm_locale";

// The display name of each language lives in the message bundles: PLAN.md R9
// keeps non-ASCII out of the source, and a switcher should name each language
// the way that language does.
export const LOCALE_LABEL_KEYS: Record<Locale, string> = {
  en: "settings.langEn",
  "pt-BR": "settings.langPtBR",
  es: "settings.langEs",
};

// Bundled at build time rather than fetched: these are small, needed on first
// paint, and a network round trip would flash untranslated text.
export const MESSAGES: Record<Locale, Messages> = {
  en: en as Messages,
  "pt-BR": ptBR as Messages,
  es: es as Messages,
};

export type Messages = Record<string, Record<string, string>>;

export function isLocale(value: string | undefined | null): value is Locale {
  return !!value && (LOCALES as readonly string[]).includes(value);
}

/**
 * Picks the best supported locale from an Accept-Language header.
 * Quality values are honoured, and a bare "pt" matches "pt-BR" so a Brazilian
 * browser that only advertises "pt" is not silently served English.
 */
export function negotiateLocale(acceptLanguage: string | null): Locale {
  if (!acceptLanguage) return DEFAULT_LOCALE;

  const ranked = acceptLanguage
    .split(",")
    .map((part) => {
      const [tag, ...params] = part.trim().split(";");
      const q = params.find((p) => p.trim().startsWith("q="));
      return { tag: tag.trim().toLowerCase(), q: q ? Number(q.split("=")[1]) || 0 : 1 };
    })
    .sort((a, b) => b.q - a.q);

  for (const { tag } of ranked) {
    const exact = LOCALES.find((l) => l.toLowerCase() === tag);
    if (exact) return exact;
    const base = tag.split("-")[0];
    const prefixed = LOCALES.find((l) => l.toLowerCase().split("-")[0] === base);
    if (prefixed) return prefixed;
  }
  return DEFAULT_LOCALE;
}

/**
 * Resolves a dotted key such as "auth.signInButton".
 *
 * A missing key falls back to English and, failing that, returns the key
 * itself: a screen showing "settings.title" is a visible bug report, whereas
 * throwing would blank the page over a typo in a label.
 */
export function translate(messages: Messages, key: string): string {
  const [namespace, name] = key.split(".");
  const value = messages?.[namespace]?.[name];
  if (typeof value === "string") return value;
  const fallback = MESSAGES[DEFAULT_LOCALE]?.[namespace]?.[name];
  return typeof fallback === "string" ? fallback : key;
}
