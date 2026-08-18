"use client";

import React, { useCallback, useEffect, useState } from "react";
import { Globe, RefreshCw, Plus, KeyRound, CheckCircle2, AlertTriangle } from "lucide-react";
import { useTranslations } from "../../../../i18n/provider";
import { Badge } from "../../../../components/ui/Badge";
import { Button } from "../../../../components/ui/Button";

/**
 * Domains, read from the database.
 *
 * The previous version of this screen was a mockup end to end. It rendered a
 * fixed table of thirteen DNS records for "example.com", every one of them
 * marked VERIFIED; its onboarding wizard had four steps of which only the first
 * called anything, step three printing "Cloudflare connection validated, 13
 * records created and propagated" without a request behind it; and its DKIM
 * rotation posted {domain_id: "default-domain"} to an endpoint whose field is
 * "domain", swallowed the resulting 400, and displayed a made-up TXT record as
 * though a key had been issued. Rotation therefore never once worked from here.
 *
 * What is shown now is what the API returns, and the one thing the backend
 * cannot yet do - verify individual DNS records against a resolver - says so
 * instead of drawing a table of green ticks.
 */

interface DnsRecordCheck {
  type: string;
  name: string;
  expected: string;
  verified: boolean;
  detail: string;
  proxied: boolean;
}

interface DnsVerification {
  domain: string;
  status: string;
  verified: number;
  total: number;
  records: DnsRecordCheck[];
}

interface Domain {
  id: string;
  name: string;
  dns_status: string;
  dmarc_policy: string | null;
  mta_sts_mode: string | null;
  dane_enabled: boolean;
  is_active: boolean;
  dns_last_checked_at: string | null;
  mailbox_count: number;
}

function statusVariant(status: string): "success" | "warning" | "danger" {
  if (status === "VERIFIED") return "success";
  if (status === "PENDING" || status === "PARTIAL") return "warning";
  return "danger";
}

export default function AdminDomainsPage() {
  const t = useTranslations();

  const [activeTab, setActiveTab] = useState<"domains" | "onboarding" | "dkim">("domains");
  const [domains, setDomains] = useState<Domain[]>([]);
  const [selectedId, setSelectedId] = useState<string>("");
  const [loading, setLoading] = useState(true);
  const [notice, setNotice] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const [reconciling, setReconciling] = useState(false);
  const [verification, setVerification] = useState<DnsVerification | null>(null);
  const [onboardDomain, setOnboardDomain] = useState("");
  const [onboarding, setOnboarding] = useState(false);

  const [dkimAlgorithm, setDkimAlgorithm] = useState<"ed25519" | "rsa2048">("ed25519");
  const [dkimSelector, setDkimSelector] = useState("s1");
  const [dkimRecord, setDkimRecord] = useState<{ name: string; value: string; overlapDays: number } | null>(null);
  const [rotating, setRotating] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await fetch("/api/v1/admin/domains");
      if (!res.ok) throw new Error();
      const data: Domain[] = await res.json();
      setDomains(data);
      // Keeps the current pick if it still exists, so a refresh after an action
      // does not silently retarget the next operation at another domain.
      setSelectedId((prev) => (data.some((d) => d.id === prev) ? prev : (data[0]?.id ?? "")));
      setError(null);
    } catch {
      setDomains([]);
      setError(t("errors.loadFailed"));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void load();
  }, [load]);

  const selected = domains.find((d) => d.id === selectedId) ?? null;

  // Asks the protocols service to resolve every expected record and report
  // each one. It used to post to the auth service, which has no resolver and
  // could only re-read the status already in the database.
  const handleReconcile = async () => {
    if (!selected) return;
    setReconciling(true);
    setNotice(null);
    setError(null);
    setVerification(null);
    try {
      const res = await fetch(`/api/v1/admin/dns/verify?domain=${encodeURIComponent(selected.name)}`);
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.message);
      setVerification(data);
      // Reconciliation still runs, so the stored status and the audit trail
      // stay in step with what was just observed.
      await fetch("/api/v1/admin/domains/reconcile", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ domain_id: selected.id }),
      }).catch(() => undefined);
      await load();
    } catch (err) {
      setError(err instanceof Error && err.message ? err.message : t("errors.serverError"));
    } finally {
      setReconciling(false);
    }
  };

  const handleOnboard = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!onboardDomain.trim()) return;
    setOnboarding(true);
    setNotice(null);
    setError(null);
    try {
      const res = await fetch("/api/v1/admin/domains/onboard", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ domain: onboardDomain.trim() }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.message);
      setOnboardDomain("");
      setNotice(`${data.name}`);
      await load();
      setActiveTab("domains");
    } catch (err) {
      setError(err instanceof Error && err.message ? err.message : t("errors.serverError"));
    } finally {
      setOnboarding(false);
    }
  };

  const handleRotateDkim = async () => {
    if (!selected) return;
    setRotating(true);
    setNotice(null);
    setError(null);
    setDkimRecord(null);
    try {
      // The endpoint's field is "domain" and it wants the name. Sending the
      // wrong field is what made every previous rotation fail unnoticed.
      const res = await fetch("/api/v1/admin/dkim/rotate", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          domain: selected.name,
          selector: dkimSelector,
          algorithm: dkimAlgorithm,
        }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.message);
      // The key that was actually generated and sealed, not a sample string.
      setDkimRecord({
        name: `${data.selector}._domainkey.${data.domain}`,
        value: data.public_key_record,
        overlapDays: data.overlap_days,
      });
    } catch (err) {
      setError(err instanceof Error && err.message ? err.message : t("errors.serverError"));
    } finally {
      setRotating(false);
    }
  };

  const tabs = [
    { id: "domains" as const, label: t("admin.domainsTitle"), icon: Globe },
    { id: "onboarding" as const, label: t("common.add"), icon: Plus },
    { id: "dkim" as const, label: "DKIM", icon: KeyRound },
  ];

  const panel = "flex flex-col gap-4 rounded-2xl bg-dark-panel p-[18px] shadow-edge";
  const input =
    "w-full min-h-[36px] rounded-[10px] bg-dark-card px-3 py-2 text-[13.5px] text-slate-100 placeholder-slate-500 shadow-edge transition-shadow focus:outline-none focus-visible:shadow-edge-accent";
  const fieldLabel = "mb-1.5 block text-xs text-slate-400";

  return (
    <div className="mx-auto flex w-full max-w-[1060px] flex-col gap-[18px] px-5 pb-11 pt-7 sm:px-8">
      <div>
        <h1 className="text-[25px] font-medium leading-tight text-slate-100">{t("admin.domainsTitle")}</h1>
        <p className="mt-1.5 text-[13.5px] text-slate-400">{t("admin.verifyIntro")}</p>
      </div>

      <div className="lm-tabstrip self-start">
        {tabs.map((tab) => {
          const Icon = tab.icon;
          return (
            <button
              key={tab.id}
              type="button"
              onClick={() => setActiveTab(tab.id)}
              data-active={activeTab === tab.id}
              aria-pressed={activeTab === tab.id}
              className="lm-tab"
            >
              <Icon className="h-[15px] w-[15px] flex-none" />
              <span>{tab.label}</span>
            </button>
          );
        })}
      </div>

      {notice && (
        <div className="flex items-center gap-2 rounded-xl bg-dark-card px-4 py-3 text-[12.5px] text-slate-200 shadow-edge">
          <CheckCircle2 className="h-4 w-4 flex-none text-indigo-500" />
          <span className="break-words">{notice}</span>
        </div>
      )}
      {error && (
        <div className="rounded-xl bg-rose-900/60 px-4 py-3 text-[12.5px] leading-relaxed text-rose-200 shadow-edge">
          {error}
        </div>
      )}

      {activeTab === "domains" && (
        <div className="flex flex-col gap-3">
          {domains.length === 0 && !loading ? (
            <div className="rounded-2xl bg-dark-panel p-8 text-center text-[13px] text-slate-400 shadow-edge">
              {t("admin.noDomains")}
            </div>
          ) : (
            domains.map((d) => (
              <div
                key={d.id}
                className="flex flex-col gap-3.5 rounded-2xl bg-dark-panel px-[18px] py-4 shadow-edge"
              >
                <div className="flex flex-wrap items-center gap-3.5">
                  <div className="min-w-[200px] flex-1">
                    <div className="break-words text-lg font-medium leading-tight text-slate-100">{d.name}</div>
                    <div className="mt-1 text-[12.5px] leading-relaxed text-slate-400">
                      {t("admin.mailboxCount", { count: d.mailbox_count })}
                      {d.dmarc_policy ? ` \u00b7 DMARC: ${d.dmarc_policy}` : ""}
                      {d.mta_sts_mode ? ` \u00b7 MTA-STS: ${d.mta_sts_mode}` : ""}
                      {d.dane_enabled ? " \u00b7 DANE" : ""}
                    </div>
                  </div>
                  <div className="flex flex-none flex-wrap items-center gap-2.5">
                    <Badge variant={statusVariant(d.dns_status)}>{d.dns_status}</Badge>
                    <Button
                      variant={selectedId === d.id ? "primary" : "secondary"}
                      size="sm"
                      onClick={() => setSelectedId(d.id)}
                    >
                      {selectedId === d.id ? <CheckCircle2 className="h-3.5 w-3.5" /> : null}
                      <span>{selectedId === d.id ? t("common.status") : t("common.add")}</span>
                    </Button>
                  </div>
                </div>
                <div className="text-[11.5px] text-slate-500">
                  {t("admin.lastChecked", {
                    when: d.dns_last_checked_at
                      ? new Date(d.dns_last_checked_at).toLocaleString()
                      : t("common.never"),
                  })}
                </div>
              </div>
            ))
          )}

          <section className={panel}>
            <div className="flex flex-wrap items-center justify-between gap-3">
              <h2 className="text-[17px] font-medium leading-tight text-slate-100">
                {selected ? t("admin.dnsRecordsFor", { domain: selected.name }) : t("ui.domainVerification")}
              </h2>
              <Button variant="secondary" size="sm" onClick={handleReconcile} disabled={reconciling || !selected}>
                <RefreshCw className={`h-3.5 w-3.5 ${reconciling ? "animate-spin" : ""}`} />
                <span>{t("admin.reconcileDns")}</span>
              </Button>
            </div>

            {verification && (
              <>
                <div className="flex flex-wrap items-center gap-3">
                  <span className="font-mono text-2xl font-medium tabular-nums text-slate-100">
                    {verification.verified} / {verification.total}
                  </span>
                  <Badge variant={statusVariant(verification.status)}>{verification.status}</Badge>
                </div>

                <div className="overflow-x-auto">
                  <table className="lm-table">
                    <thead>
                      <tr>
                        <th className="pl-0">{t("ui.recordName")}</th>
                        <th>{t("ui.expectedValue")}</th>
                        <th className="pr-0 text-right">{t("common.status")}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {verification.records.map((rec, i) => (
                        <tr key={i} className="align-top">
                          <td className="whitespace-nowrap pl-0 text-[13px]">
                            <Badge variant="neutral" className="mr-2 font-mono">
                              {rec.type}
                            </Badge>
                            <span className="font-mono text-[12.5px] text-slate-200">{rec.name}</span>
                          </td>
                          <td className="max-w-[420px] break-all font-mono text-xs text-slate-400">
                            {rec.expected}
                          </td>
                          <td className="whitespace-nowrap pr-0 text-right">
                            {rec.verified ? (
                              <span className="inline-flex items-center gap-1.5 text-[12.5px] text-indigo-400">
                                <CheckCircle2 className="h-3.5 w-3.5" />
                                {rec.proxied ? t("admin.presentProxied") : t("admin.dnsStatusVerified")}
                              </span>
                            ) : (
                              <span
                                className="inline-flex items-center gap-1.5 text-[12.5px] text-amber-400"
                                title={rec.detail}
                              >
                                <AlertTriangle className="h-3.5 w-3.5" />
                                {t("admin.notPublished")}
                              </span>
                            )}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </>
            )}
          </section>
        </div>
      )}

      {activeTab === "onboarding" && (
        <section className={`${panel} max-w-[620px]`}>
          <div>
            <h2 className="text-[17px] font-medium leading-tight text-slate-100">{t("admin.onboardTitle")}</h2>
            <p className="mt-1 text-[13px] leading-relaxed text-slate-400">{t("admin.onboardIntro")}</p>
          </div>
          <form onSubmit={handleOnboard} className="flex flex-col gap-4">
            <div>
              <label htmlFor="onboard-domain" className={fieldLabel}>
                {t("admin.domainName")}
              </label>
              <input
                id="onboard-domain"
                type="text"
                value={onboardDomain}
                onChange={(e) => setOnboardDomain(e.target.value)}
                placeholder="example.org"
                required
                className={input}
              />
            </div>
            <Button type="submit" variant="primary" size="md" className="self-start" disabled={onboarding}>
              <Plus className="h-4 w-4" />
              <span>{onboarding ? t("common.loading") : t("common.add")}</span>
            </Button>
          </form>
        </section>
      )}

      {activeTab === "dkim" && (
        <section className={`${panel} max-w-[720px]`}>
          <div>
            <h2 className="flex items-center gap-2 text-[17px] font-medium leading-tight text-slate-100">
              <KeyRound className="h-[17px] w-[17px] flex-none text-indigo-500" />
              {t("admin.dkimTitle")}
            </h2>
            <p className="mt-1 text-[13px] text-slate-400">{selected ? selected.name : t("admin.noDomains")}</p>
          </div>

          <div className="flex flex-wrap gap-3">
            <div className="min-w-[200px] flex-1">
              <label htmlFor="dkim-algorithm" className={fieldLabel}>
                {t("admin.dkimAlgorithm")}
              </label>
              <select
                id="dkim-algorithm"
                value={dkimAlgorithm}
                onChange={(e) => setDkimAlgorithm(e.target.value as "ed25519" | "rsa2048")}
                className={input}
              >
                <option value="ed25519">Ed25519</option>
                <option value="rsa2048">RSA-2048</option>
              </select>
            </div>
            <div className="min-w-[200px] flex-1">
              <label htmlFor="dkim-selector" className={fieldLabel}>
                {t("admin.dkimSelector")}
              </label>
              <input
                id="dkim-selector"
                type="text"
                value={dkimSelector}
                onChange={(e) => setDkimSelector(e.target.value)}
                pattern="[A-Za-z0-9-]{1,63}"
                className={`${input} font-mono`}
              />
            </div>
          </div>

          <Button
            variant="primary"
            size="md"
            className="self-start"
            onClick={handleRotateDkim}
            disabled={rotating || !selected}
          >
            <KeyRound className="h-4 w-4" />
            <span>{rotating ? t("common.loading") : t("admin.dkimRotate")}</span>
          </Button>

          {dkimRecord && (
            <div className="flex flex-col gap-2">
              <div className="text-xs text-slate-400">{t("admin.dkimPublishRecord")}</div>
              <div className="lm-code break-all px-3.5 py-3">
                {dkimRecord.name} IN TXT &quot;{dkimRecord.value}&quot;
              </div>
              <div className="flex items-start gap-2.5 rounded-xl bg-dark-card px-3.5 py-3 shadow-edge">
                <AlertTriangle className="mt-px h-4 w-4 flex-none text-amber-400" />
                <span className="text-[12.5px] leading-relaxed text-slate-300">
                  {t("admin.dkimOverlap", { days: dkimRecord.overlapDays })}
                </span>
              </div>
            </div>
          )}
        </section>
      )}
    </div>
  );
}
