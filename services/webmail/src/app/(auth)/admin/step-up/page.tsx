"use client";

import React, { useState } from "react";
import Link from "next/link";
import { KeyRound, ShieldCheck, AlertCircle, Sliders } from "lucide-react";
import { useTranslations } from "../../../../i18n/provider";
import { useAccount } from "../../../../lib/useAccount";
import { TotpEnrolment, RecoveryCodes } from "../../../../components/TotpEnrolment";
import { Button } from "../../../../components/ui/Button";

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
    <div className="flex min-h-screen items-center justify-center bg-dark-bg px-4">
      <div className="w-full max-w-[400px] rounded-2xl bg-dark-panel p-7 shadow-edge">
        <div className="mb-6 flex items-center gap-3">
          <div className="flex h-10 w-10 flex-none items-center justify-center rounded-xl bg-dark-card text-indigo-500 shadow-[inset_0_0_0_1px_#9184d9]">
            <Sliders className="h-5 w-5" />
          </div>
          <div className="min-w-0">
            <h1 className="text-lg font-medium leading-tight text-slate-100">{t("auth.stepUpTitle")}</h1>
            <p className="mt-0.5 break-words text-[12.5px] leading-relaxed text-slate-400">
              {account?.email ? t("auth.stepUpSignedInAs", { email: account.email }) : t("common.loading")}
            </p>
          </div>
        </div>

        {error && (
          <div className="mb-4 flex items-start gap-2.5 rounded-xl bg-rose-900/60 p-3.5 text-xs leading-relaxed text-rose-200 shadow-edge">
            <AlertCircle className="mt-px h-4 w-4 flex-none" />
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
          <form onSubmit={submit} className="flex flex-col gap-4">
            {enrolSecret ? (
              <div className="flex flex-col gap-4">
                <div className="rounded-xl bg-dark-card p-3.5 text-xs leading-relaxed text-slate-300 shadow-edge">
                  {t("auth.enrolmentIntro")}
                </div>
                <TotpEnrolment secret={enrolSecret} uri={enrolUri} />
              </div>
            ) : (
              <div className="flex items-start gap-2.5 rounded-xl bg-dark-card p-3.5 text-xs leading-relaxed text-slate-300 shadow-edge">
                <ShieldCheck className="mt-px h-4 w-4 flex-none text-indigo-500" />
                <span>{t("auth.stepUpIntro")}</span>
              </div>
            )}

            <div>
              <label htmlFor="step-up-code" className="mb-1.5 block text-xs text-slate-400">
                {t("auth.totpCodeLabel")}
              </label>
              <div className="relative">
                <KeyRound className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" />
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
                  className="w-full rounded-xl bg-dark-card py-3 pl-[38px] pr-3 text-center font-mono text-lg tracking-[0.3em] text-slate-100 placeholder-slate-500 shadow-edge transition-shadow focus:outline-none focus-visible:shadow-edge-accent"
                />
              </div>
              {/* A recovery code is the way in when the phone is gone, and
                  nothing on the old screens said so. */}
              <p className="mt-1.5 text-[11.5px] leading-relaxed text-slate-500">{t("auth.orRecoveryCode")}</p>
            </div>

            <Button type="submit" variant="primary" size="lg" className="w-full" disabled={loading}>
              {loading ? t("common.loading") : t("auth.verifyCodeButton")}
            </Button>
          </form>
        )}

        <div className="mt-6 pt-4 text-center">
          <div className="lm-rule mb-4" />
          <Link href="/user/mail/inbox" className="text-xs text-slate-400 transition-colors hover:text-slate-200">
            {t("admin.backToWebmail")}
          </Link>
        </div>
      </div>
    </div>
  );
}
