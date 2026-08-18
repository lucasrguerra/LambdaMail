"use client";

import React, { useMemo, useState } from "react";
import qrcode from "qrcode-generator";
import { useTranslations } from "../i18n/provider";

/**
 * The scannable half of TOTP enrolment, shared by the webmail settings page and
 * the admin sign-in step-up so both offer the same thing.
 *
 * Previously each screen printed the base32 secret as text and left the
 * operator to type 32 characters into their phone by hand. Every authenticator
 * app expects to scan an otpauth:// URI, and the enrol endpoint already returns
 * one - it simply was not being drawn.
 *
 * The QR is rendered locally rather than fetched: the secret is already in this
 * page's memory, and sending it to an image service - even our own - would put
 * a second factor's seed into another request log for no benefit.
 */

/**
 * Builds an <img>-ready data URI for the given otpauth URI.
 *
 * Type number 0 lets the library pick the smallest QR version that fits, and
 * level M tolerates ~15% damage, which is the usual choice for screen-displayed
 * codes that may be scanned at an angle.
 */
function qrDataUri(text: string): string | null {
  try {
    const qr = qrcode(0, "M");
    qr.addData(text);
    qr.make();
    // Module size 6 gives a crisp code in the ~200px box below without scaling
    // a blurry bitmap up. The margin is the 4-module quiet zone the spec
    // requires: baking it into the image means the code still scans even if the
    // surrounding styling changes.
    return qr.createDataURL(6, 4);
  } catch {
    // An over-long URI is the only realistic failure. The manual key below is a
    // complete fallback, so a missing image must not take the screen down.
    return null;
  }
}

export function TotpEnrolment({ secret, uri }: { secret: string; uri: string | null }) {
  const t = useTranslations();
  const [copied, setCopied] = useState(false);
  const dataUri = useMemo(() => (uri ? qrDataUri(uri) : null), [uri]);

  const copySecret = async () => {
    try {
      await navigator.clipboard.writeText(secret);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // Clipboard access can be refused; the key stays selectable on screen.
    }
  };

  return (
    <div className="space-y-4">
      {dataUri && (
        <div className="flex flex-col items-center gap-3">
          <p className="text-xs text-slate-300 text-center">{t("auth.scanQr")}</p>
          {/* A QR must stay light-on-dark-free to scan: the white quiet zone is
              part of the format, so this box keeps its own background. */}
          <div className="rounded-xl bg-white p-3">
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img
              src={dataUri}
              alt={t("auth.qrCodeAlt")}
              width={200}
              height={200}
              className="block h-[200px] w-[200px]"
            />
          </div>
        </div>
      )}

      <div className="space-y-1.5">
        <p className="text-xs text-slate-400">{t("auth.cantScanQr")}</p>
        <div className="flex items-center gap-2">
          <code className="lm-code min-w-0 flex-1 break-all p-2.5">
            {secret}
          </code>
          <button
            type="button"
            onClick={() => void copySecret()}
            className="flex-none rounded-[10px] border border-white/[0.14] px-3 py-2 text-xs text-slate-200 transition-colors hover:bg-white/[0.07] hover:text-slate-100"
          >
            {copied ? t("common.copied") : t("common.copy")}
          </button>
        </div>
      </div>
    </div>
  );
}

/**
 * The one-time recovery codes, with the two distinct actions the screen needs.
 *
 * The admin sign-in previously rendered its "sign in" button here *and* kept
 * the form's own submit button visible underneath, so this step showed two
 * identical buttons and no way to copy the codes it was telling you to save.
 */
export function RecoveryCodes({
  codes,
  onContinue,
  continueLabel,
}: {
  codes: string[];
  onContinue: () => void;
  continueLabel: string;
}) {
  const t = useTranslations();
  const [copied, setCopied] = useState(false);

  const copyAll = async () => {
    try {
      await navigator.clipboard.writeText(codes.join("\n"));
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // Same as above: the codes remain on screen and selectable.
    }
  };

  return (
    <div className="space-y-3">
      <div className="rounded-xl bg-dark-card p-3.5 text-xs text-slate-300 shadow-edge">
        <div className="mb-1 text-[13.5px] font-medium text-slate-100">{t("settings.recoveryCodesTitle")}</div>
        <p className="mb-3 leading-relaxed">{t("settings.saveRecoveryCodes")}</p>
        <div className="lm-code grid grid-cols-2 gap-1.5 p-3 text-center text-[11px] sm:grid-cols-3">
          {codes.map((code) => (
            <span key={code} className="rounded bg-white/[0.05] p-1">
              {code}
            </span>
          ))}
        </div>
      </div>

      <div className="flex gap-2">
        <button
          type="button"
          onClick={() => void copyAll()}
          className="flex-1 rounded-[10px] border border-white/[0.14] py-2.5 text-sm font-medium text-slate-200 transition-colors hover:bg-white/[0.07]"
        >
          {copied ? t("common.copied") : t("common.copy")}
        </button>
        <button
          type="button"
          onClick={onContinue}
          className="flex-1 rounded-[10px] border border-indigo-500 py-2.5 text-sm font-medium text-indigo-500 transition-colors hover:bg-indigo-500/[0.12] active:bg-indigo-500/[0.22]"
        >
          {continueLabel}
        </button>
      </div>
    </div>
  );
}
