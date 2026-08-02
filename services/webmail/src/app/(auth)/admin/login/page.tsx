"use client";

import React, { useState } from "react";
import Link from "next/link";
import { useTranslations } from "../../../../i18n/provider";
import { TotpEnrolment, RecoveryCodes } from "../../../../components/TotpEnrolment";

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

  return (
    <div className="min-h-screen flex items-center justify-center px-4 bg-slate-950">
      <div className="glass-panel p-8 rounded-2xl max-w-md w-full border border-emerald-900/40 shadow-2xl">
        <div className="flex items-center gap-3 mb-6">
          <div className="w-10 h-10 rounded-xl bg-emerald-600/20 border border-emerald-500/30 flex items-center justify-center text-emerald-400 font-bold text-xl">
            &#9881;
          </div>
          <div>
            <h1 className="text-xl font-bold text-white">{t("auth.adminLoginTitle")}</h1>
            <p className="text-xs text-slate-400">{t("auth.adminStepUpNote")}</p>
          </div>
        </div>

        {error && (
          <div className="mb-4 p-3 rounded-lg bg-red-500/10 border border-red-500/30 text-red-400 text-sm">
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
        <form onSubmit={handleLogin} className="space-y-4">
          {enrolSecret ? (
            <div className="space-y-4">
              <div className="rounded-lg border border-indigo-500/30 bg-indigo-500/10 p-3 text-xs text-indigo-300">
                {t("auth.enrolmentIntro")}
              </div>
              <TotpEnrolment secret={enrolSecret} uri={enrolUri} />
              <div>
                <label className="mb-1 block text-xs font-medium text-slate-300">{t("auth.totpCodeLabel")}</label>
                <input
                  type="text"
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  value={mfaCode}
                  onChange={(e) => setMfaCode(e.target.value)}
                  maxLength={6}
                  required
                  className="w-full rounded-lg border border-slate-800 bg-slate-900 px-4 py-2.5 text-center font-mono text-lg tracking-widest text-white focus:border-emerald-500 focus:outline-none"
                />
              </div>
            </div>
          ) : !challengeToken ? (
            <>
              <div>
                <label className="block text-xs font-medium text-slate-300 mb-1">{t("auth.emailLabel")}</label>
                <input
                  type="email"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder="admin@domain.com"
                  required
                  className="w-full px-4 py-2.5 rounded-lg bg-slate-900 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-emerald-500 transition-colors"
                />
              </div>

              <div>
                <label className="block text-xs font-medium text-slate-300 mb-1">{t("auth.passwordLabel")}</label>
                <input
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder="************"
                  required
                  className="w-full px-4 py-2.5 rounded-lg bg-slate-900 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-emerald-500 transition-colors"
                />
              </div>
            </>
          ) : (
            <div>
              <div className="p-3 mb-4 rounded-lg bg-emerald-500/10 border border-emerald-500/30 text-emerald-300 text-xs">
                {t("auth.mfaRequired")}
              </div>
              <label className="block text-xs font-medium text-slate-300 mb-1">{t("auth.totpCodeLabel")}</label>
              <input
                type="text"
                value={mfaCode}
                onChange={(e) => setMfaCode(e.target.value)}
                placeholder="123456"
                maxLength={6}
                required
                className="w-full px-4 py-2.5 rounded-lg bg-slate-900 border border-slate-800 text-white text-center tracking-widest text-lg font-mono focus:outline-none focus:border-emerald-500 transition-colors"
              />
            </div>
          )}

          <button
            type="submit"
            disabled={loading}
            className="w-full py-3 rounded-xl bg-emerald-600 hover:bg-emerald-500 text-white font-medium transition-colors shadow-lg shadow-emerald-600/20 disabled:opacity-50"
          >
            {loading
              ? t("common.loading")
              : challengeToken || enrolSecret
                ? t("auth.verifyCodeButton")
                : t("auth.signInButton")}
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
