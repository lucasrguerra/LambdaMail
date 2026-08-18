"use client";

import React, { useState } from "react";
import Link from "next/link";
import { useTranslations } from "../../../../i18n/provider";
import { TotpEnrolment, RecoveryCodes } from "../../../../components/TotpEnrolment";
import { Button } from "../../../../components/ui/Button";

export default function AdminLoginPage() {
  const t = useTranslations();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [mfaCode, setMfaCode] = useState("");
  const [challengeToken, setChallengeToken] = useState<string | null>(null);
  // Enrolment state: the console requires a second factor, so an account
  // without one is walked through creating it here rather than being told to
  // go and find another screen.
  const [enrolmentToken, setEnrolmentToken] = useState<string | null>(null);
  const [enrolSecret, setEnrolSecret] = useState<string | null>(null);
  const [enrolUri, setEnrolUri] = useState<string | null>(null);
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const handleLogin = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    setLoading(true);

    try {
      if (enrolmentToken) {
        const res = await fetch("/api/v1/user/mfa/totp/confirm", {
          method: "POST",
          headers: { "Content-Type": "application/json", Authorization: `Bearer ${enrolmentToken}` },
          body: JSON.stringify({ code: mfaCode }),
        });
        const data = await res.json();
        if (!res.ok) throw new Error(data.message || t("auth.verificationFailed"));
        // Shown once. The operator signs in again with the factor they just
        // created, which is also the first proof that it works.
        setRecoveryCodes(data.recovery_codes ?? []);
        setEnrolmentToken(null);
        setEnrolSecret(null);
        setMfaCode("");
        return;
      }

      if (challengeToken) {
        // Step 2: Mandatory Admin 2FA Code
        const res = await fetch("/api/v1/auth/mfa/verify", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ challenge_token: challengeToken, code: mfaCode }),
        });
        const data = await res.json();
        if (!res.ok) throw new Error(data.message || t("auth.verificationFailed"));
        window.location.href = "/admin/dashboard";
        return;
      }

      // Step 1: Admin credentials
      const res = await fetch("/api/v1/auth/admin/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, password }),
      });
      const data = await res.json();

      // The password was right but no second factor exists. The response
      // carries a grant that permits enrolling one and nothing else.
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
        return;
      }

      if (!res.ok) throw new Error(data.message || t("auth.authFailed"));

      if (data.mfa_required) {
        setChallengeToken(data.challenge_token);
      } else {
        window.location.href = "/admin/dashboard";
      }
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : t("auth.genericError"));
    } finally {
      setLoading(false);
    }
  };

  const field =
    "w-full min-h-[38px] rounded-xl bg-dark-card px-3 py-2.5 text-[13.5px] text-slate-100 placeholder-slate-500 shadow-edge transition-shadow focus:outline-none focus-visible:shadow-edge-accent";
  const codeField = `${field} text-center font-mono text-lg tracking-[0.3em]`;
  const fieldLabel = "mb-1.5 block text-xs text-slate-400";

  return (
    <div className="flex min-h-screen items-center justify-center bg-dark-bg px-4">
      <div className="w-full max-w-[400px] rounded-2xl bg-dark-panel p-7 shadow-edge">
        {/* The console sign-in wears the same mark as the webmail. It used to be
            green over green, which made the second surface look like a
            different product. */}
        <div className="mb-6 flex items-center gap-3">
          <div className="flex h-10 w-10 flex-none items-center justify-center rounded-xl bg-dark-card text-xl leading-none text-indigo-500 shadow-[inset_0_0_0_1px_#9184d9]">
            λ
          </div>
          <div className="min-w-0">
            <h1 className="text-lg font-medium leading-tight text-slate-100">{t("auth.adminLoginTitle")}</h1>
            <p className="mt-0.5 text-[12.5px] leading-relaxed text-slate-400">{t("auth.adminStepUpNote")}</p>
          </div>
        </div>

        {error && (
          <div className="mb-4 rounded-xl bg-rose-900/60 p-3.5 text-xs leading-relaxed text-rose-200 shadow-edge">
            {error}
          </div>
        )}

        {/* The recovery-code step is terminal: it owns its own buttons, so the
            form's submit button is not rendered underneath it. That overlap is
            what previously put two identical "sign in" buttons on the screen
            and left no way to copy the codes it insisted you save. */}
        {recoveryCodes ? (
          <RecoveryCodes
            codes={recoveryCodes}
            onContinue={() => setRecoveryCodes(null)}
            continueLabel={t("auth.signInButton")}
          />
        ) : (
          <form onSubmit={handleLogin} className="flex flex-col gap-4">
            {enrolSecret ? (
              <div className="flex flex-col gap-4">
                <div className="rounded-xl bg-dark-card p-3.5 text-xs leading-relaxed text-slate-300 shadow-edge">
                  {t("auth.enrolmentIntro")}
                </div>
                <TotpEnrolment secret={enrolSecret} uri={enrolUri} />
                <div>
                  <label htmlFor="admin-enrol-code" className={fieldLabel}>
                    {t("auth.totpCodeLabel")}
                  </label>
                  <input
                    id="admin-enrol-code"
                    type="text"
                    inputMode="numeric"
                    autoComplete="one-time-code"
                    value={mfaCode}
                    onChange={(e) => setMfaCode(e.target.value.replace(/\D/g, "").slice(0, 6))}
                    maxLength={7}
                    required
                    className={codeField}
                  />
                </div>
              </div>
            ) : !challengeToken ? (
              <>
                <div>
                  <label htmlFor="admin-email" className={fieldLabel}>
                    {t("auth.emailLabel")}
                  </label>
                  <input
                    id="admin-email"
                    type="email"
                    autoComplete="username"
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                    placeholder="admin@domain.com"
                    required
                    className={field}
                  />
                </div>

                <div>
                  <label htmlFor="admin-password" className={fieldLabel}>
                    {t("auth.passwordLabel")}
                  </label>
                  <input
                    id="admin-password"
                    type="password"
                    autoComplete="current-password"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    placeholder="************"
                    required
                    className={field}
                  />
                </div>
              </>
            ) : (
              <div className="flex flex-col gap-4">
                <div className="rounded-xl bg-dark-card p-3.5 text-xs leading-relaxed text-slate-300 shadow-edge">
                  {t("auth.mfaRequired")}
                </div>
                <div>
                  <label htmlFor="admin-mfa-code" className={fieldLabel}>
                    {t("auth.totpCodeLabel")}
                  </label>
                  <input
                    id="admin-mfa-code"
                    type="text"
                    inputMode="numeric"
                    autoComplete="one-time-code"
                    value={mfaCode}
                    onChange={(e) => setMfaCode(e.target.value.replace(/\D/g, "").slice(0, 6))}
                    placeholder="123456"
                    maxLength={7}
                    required
                    className={codeField}
                  />
                </div>
              </div>
            )}

            <Button type="submit" variant="primary" size="lg" className="mt-1 w-full" disabled={loading}>
              {loading
                ? t("common.loading")
                : challengeToken || enrolSecret
                  ? t("auth.verifyCodeButton")
                  : t("auth.signInButton")}
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
