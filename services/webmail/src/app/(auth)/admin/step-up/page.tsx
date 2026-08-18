"use client";

import React, { useState } from "react";
import Link from "next/link";
import { KeyRound, ShieldCheck, AlertCircle, Sliders } from "lucide-react";
import { useTranslations } from "../../../../i18n/provider";
import { useAccount } from "../../../../lib/useAccount";
import { TotpEnrolment, RecoveryCodes } from "../../../../components/TotpEnrolment";

/**
 * Crossing from the webmail into the console.
 *
 * The password is not asked for again - the session that got here proved it -
 * so this collects the one thing the console adds over the webmail: the second
 * factor. Sending an operator who was already signed in to a full sign-in form
 * was the app forgetting what it had already been told.
 *
 * An account with no factor yet is walked through enrolling one here rather
 * than being sent off to find the settings screen.
 */
export default function AdminStepUpPage() {
  const t = useTranslations();
  const account = useAccount("user");

  const [code, setCode] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [enrolmentToken, setEnrolmentToken] = useState<string | null>(null);
  const [enrolSecret, setEnrolSecret] = useState<string | null>(null);
  const [enrolUri, setEnrolUri] = useState<string | null>(null);
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null);

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    setError(null);
    setLoading(true);

    try {
      // Confirming a fresh enrolment, using the grant the step-up handed back.
      if (enrolmentToken) {
        const res = await fetch("/api/v1/user/mfa/totp/confirm", {
          method: "POST",
          headers: { "Content-Type": "application/json", Authorization: `Bearer ${enrolmentToken}` },
          body: JSON.stringify({ code }),
        });
        const data = await res.json();
        if (!res.ok) throw new Error(data.message || t("auth.verificationFailed"));
        setRecoveryCodes(data.recovery_codes ?? []);
        setEnrolmentToken(null);
        setEnrolSecret(null);
        setCode("");
        return;
      }

      const res = await fetch("/api/v1/auth/admin/step-up", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ code }),
      });
      const data = await res.json();

      // No factor enrolled. The grant that comes back permits enrolling one and
      // nothing else, so the operator can do it without leaving this screen.
      if (res.status === 403 && data.error === "MFA_ENROLLMENT_REQUIRED" && data.enrollment_token) {
        const enroll = await fetch("/api/v1/user/mfa/totp/enroll", {
          method: "POST",
          headers: { Authorization: `Bearer ${data.enrollment_token}` },
        });
        const enrolData = await enroll.json();
        if (!enroll.ok) throw new Error(enrolData.message || t("auth.enrolmentFailed"));
        setEnrolmentToken(data.enrollment_token);
        setEnrolSecret(enrolData.secret);
        setEnrolUri(enrolData.uri);
        setCode("");
        return;
      }

      // A session with no webmail session behind it cannot step up; the sign-in
      // is where that starts.
      if (res.status === 401) {
        window.location.href = "/user/login?next=/admin/step-up";
        return;
      }
      if (!res.ok) throw new Error(data.message || t("auth.verificationFailed"));

      window.location.href = "/admin/dashboard";
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : t("auth.genericError"));
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="min-h-screen flex items-center justify-center px-4 bg-slate-950">
      <div className="glass-panel p-8 rounded-2xl max-w-md w-full border border-emerald-900/40 shadow-2xl">
        <div className="flex items-center gap-3 mb-6">
          <div className="w-10 h-10 rounded-xl bg-emerald-600/20 border border-emerald-500/30 flex items-center justify-center text-emerald-400">
            <Sliders className="w-5 h-5" />
          </div>
          <div className="min-w-0">
            <h1 className="text-lg font-bold text-white">{t("auth.stepUpTitle")}</h1>
            <p className="text-xs text-slate-400 truncate">
              {account?.email ? t("auth.stepUpSignedInAs", { email: account.email }) : t("common.loading")}
            </p>
          </div>
        </div>

        {error && (
          <div className="mb-4 p-3 rounded-lg bg-rose-500/10 border border-rose-500/30 text-rose-300 text-xs flex items-center gap-2">
            <AlertCircle className="w-4 h-4 flex-shrink-0" />
            <span>{error}</span>
          </div>
        )}

        {recoveryCodes ? (
          <RecoveryCodes
            codes={recoveryCodes}
            onContinue={() => setRecoveryCodes(null)}
            continueLabel={t("common.continue")}
          />
        ) : (
          <form onSubmit={submit} className="space-y-4">
            {enrolSecret ? (
              <div className="space-y-4">
                <div className="rounded-lg border border-indigo-500/30 bg-indigo-500/10 p-3 text-xs text-indigo-300">
                  {t("auth.enrolmentIntro")}
                </div>
                <TotpEnrolment secret={enrolSecret} uri={enrolUri} />
              </div>
            ) : (
              <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/10 p-3 text-xs text-emerald-300 flex items-start gap-2">
                <ShieldCheck className="w-4 h-4 flex-shrink-0 mt-0.5" />
                <span>{t("auth.stepUpIntro")}</span>
              </div>
            )}

            <div>
              <label htmlFor="step-up-code" className="mb-1 block text-xs font-medium text-slate-300">
                {t("auth.totpCodeLabel")}
              </label>
              <div className="relative">
                <KeyRound className="w-4 h-4 absolute left-3.5 top-3.5 text-slate-500" />
                <input
                  id="step-up-code"
                  type="text"
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  autoFocus
                  value={code}
                  onChange={(e) => setCode(e.target.value.replace(/\s/g, ""))}
                  placeholder="123456"
                  required
                  className="w-full rounded-xl border border-slate-800 bg-slate-900 pl-10 pr-4 py-3 text-center font-mono text-lg tracking-widest text-white focus:border-emerald-500 focus:outline-none"
                />
              </div>
              {/* A recovery code is the way in when the phone is gone, and
                  nothing on the old screens said so. */}
              <p className="mt-1.5 text-[11px] text-slate-500">{t("auth.orRecoveryCode")}</p>
            </div>

            <button
              type="submit"
              disabled={loading}
              className="w-full py-3 rounded-xl bg-emerald-600 hover:bg-emerald-500 text-white font-medium transition-colors shadow-lg shadow-emerald-600/20 disabled:opacity-50"
            >
              {loading ? t("common.loading") : t("auth.verifyCodeButton")}
            </button>
          </form>
        )}

        <div className="mt-6 pt-4 border-t border-slate-800 text-center">
          <Link href="/user/mail/inbox" className="text-xs text-slate-400 hover:text-slate-200 transition-colors">
            {t("admin.backToWebmail")}
          </Link>
        </div>
      </div>
    </div>
  );
}
