import "./globals.css";
import React from "react";
import { cookies, headers } from "next/headers";
import { I18nProvider } from "../i18n/provider";
import { LOCALE_COOKIE, isLocale, negotiateLocale } from "../i18n/config";

export const metadata = {
  title: "LambdaMail - Secure Self-Hosted Webmail & Admin Console",
  description: "Modern, open-source email server web interface with 2FA TOTP and surface isolation.",
};

/**
 * The locale is resolved on the server so the first paint is already in the
 * right language: an explicit choice wins, otherwise the browser's
 * Accept-Language decides.
 */
async function resolveLocale() {
  const cookieStore = await cookies();
  const chosen = cookieStore.get(LOCALE_COOKIE)?.value;
  if (isLocale(chosen)) return chosen;

  const headerStore = await headers();
  return negotiateLocale(headerStore.get("accept-language"));
}

export default async function RootLayout({ children }: { children: React.ReactNode }) {
  const locale = await resolveLocale();

  return (
    <html lang={locale} className="dark">
      <body className="min-h-screen bg-dark-bg font-sans text-slate-100 antialiased">
        <I18nProvider locale={locale}>{children}</I18nProvider>
      </body>
    </html>
  );
}
