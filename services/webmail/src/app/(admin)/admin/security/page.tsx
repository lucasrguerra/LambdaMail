"use client";

import React, { useState } from "react";
import {
  ShieldCheck,
  Lock,
  Sliders,
  ListOrdered,
  CheckCircle2,
  Search,
  Shield,
  AlertTriangle,
} from "lucide-react";
import { useTranslations } from "../../../../i18n/provider";
import { Badge } from "../../../../components/ui/Badge";
import { Button } from "../../../../components/ui/Button";

interface PreflightCheck {
  id: number;
  name: string;
  category: string;
  status: "PASSED" | "FAILED" | "WARNING";
  details: string;
}

interface ApiAuditRow {
  id: number | string;
  action: string;
  target_type: string | null;
  target_id: string | null;
  actor_ip: string | null;
  actor_email: string | null;
  created_at: string | null;
}

interface AuditEntry {
  id: string;
  timestamp: string;
  queueId: string;
  actor: string;
  action: string;
  target: string;
  ip: string;
}

export default function AdminSecurityPage() {
  const t = useTranslations();

  const [activeTab, setActiveTab] = useState<"readiness" | "tls" | "rspamd" | "audit">("readiness");

  // Rspamd thresholds state
  const [greylistScore, setGreylistScore] = useState(4.0);
  const [addHeaderScore, setAddHeaderScore] = useState(6.0);
  const [rejectScore, setRejectScore] = useState(15.0);
  const [rspamdSaved, setRspamdSaved] = useState(false);

  // Queue ID Trace Search state
  const [searchQueueId, setSearchQueueId] = useState("");
  const [auditEntries, setAuditEntries] = useState<AuditEntry[]>([]);
  const [readiness, setReadiness] = useState<PreflightCheck[] | null>(null);
  const [tracedSteps, setTracedSteps] = useState<{ step: number; title: string; detail: string; status: string }[] | null>(null);

  /**
   * Live certificate state from the protocols service, which owns the cert
   * watcher. The panel this replaced was three static cards - "Let's Encrypt
   * RSA 4096", "3 1 1 SHA-256 Verified", "mode=enforce" - plus a fixed list of
   * cipher suites, none of it read from anywhere. It reported a healthy,
   * DANE-verified certificate on a deployment serving a self-signed one.
   */
  const [tlsStatus, setTlsStatus] = useState<Record<string, unknown> | null>(null);

  React.useEffect(() => {
    fetch("/api/v1/admin/rspamd/thresholds")
      .then((r) => (r.ok ? r.json() : null))
      .then((data) => {
        if (data) {
          if (typeof data.greylist === "number") setGreylistScore(data.greylist);
          if (typeof data.add_header === "number") setAddHeaderScore(data.add_header);
          if (typeof data.reject === "number") setRejectScore(data.reject);
        }
      })
      .catch(() => undefined);

    void fetch("/api/v1/admin/audit")
      .then((r) => (r.ok ? r.json() : []))
      .then((rows: ApiAuditRow[]) =>
        setAuditEntries(
          rows.map((row) => ({
            id: String(row.id),
            timestamp: row.created_at ? new Date(row.created_at).toLocaleString() : "",
            queueId: String(row.target_id ?? ""),
            actor: row.actor_email ?? "sistema",
            action: row.action,
            target: [row.target_type, row.target_id].filter(Boolean).join(" "),
            ip: row.actor_ip ?? "",
          }))
        )
      )
      .catch(() => undefined);

    void fetch("/api/v1/admin/tls")
      .then((r) => (r.ok ? r.json() : null))
      .then(setTlsStatus)
      .catch(() => undefined);

    void fetch("/api/v1/admin/preflight")
      .then((r) => (r.ok ? r.json() : null))
      .then((data) => {
        if (data && Array.isArray(data.checks)) {
          setReadiness(
            // The service sends a key and its subject, so the label is built
            // in the reader's language. It used to send a finished English
            // sentence ("DNS records for example.com") that appeared verbatim
            // in an otherwise translated console.
            data.checks.map(
              (
                c: { key?: string; target?: string; name: string; status: string; detail?: string },
                index: number,
              ) => ({
                id: index + 1,
                name: c.key && c.target ? t(`admin.${c.key}`, { domain: c.target }) : c.name,
                category: t("ui.domainVerification"),
                status: c.status === "PASS" ? "PASSED" : c.status === "FAIL" ? "FAILED" : "WARNING",
                details: c.detail ?? "",
              }),
            ) as PreflightCheck[]
          );
        }
      })
      .catch(() => undefined);
  }, []);

  const handleSaveRspamd = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await fetch("/api/v1/admin/rspamd/thresholds", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ greylist: greylistScore, add_header: addHeaderScore, reject: rejectScore }),
      });
      setRspamdSaved(true);
      setTimeout(() => setRspamdSaved(false), 3000);
    } catch {
      // handled
    }
  };

  const handleSearchQueueId = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!searchQueueId) return;
    try {
      const res = await fetch(`/api/v1/admin/logs/trace?queue_id=${encodeURIComponent(searchQueueId)}`);
      const logs = (await res.json().catch(() => [])) as Array<{ action: string; metadata?: unknown; created_at?: string }>;
      if (Array.isArray(logs) && logs.length > 0) {
        setTracedSteps(
          logs.map((l, idx) => ({
            step: idx + 1,
            title: l.action,
            detail: JSON.stringify(l.metadata ?? {}),
            status: "OK",
          }))
        );
      } else {
        // An empty result means the queue id was not found. This used to
        // invent a five-step delivery trace instead - TLS 1.3 accepted, SPF
        // pass, Rspamd score 0.8 - for a message that does not exist, which is
        // the worst possible answer to "what happened to this mail?".
        setTracedSteps([]);
      }
    } catch {
      // fallback
    }
  };

  const securityTabs = [
    { id: "readiness" as const, label: t("admin.tabReadiness"), icon: ShieldCheck },
    { id: "tls" as const, label: t("admin.tabTls"), icon: Lock },
    { id: "rspamd" as const, label: t("admin.tabRspamd"), icon: Sliders },
    { id: "audit" as const, label: t("admin.tabAudit"), icon: ListOrdered },
  ];

  const panel = "flex flex-col gap-4 rounded-2xl bg-dark-panel p-[18px] shadow-edge";
  const input =
    "w-full min-h-[36px] rounded-[10px] bg-dark-card px-3 py-2 text-[13.5px] text-slate-100 placeholder-slate-500 shadow-edge transition-shadow focus:outline-none focus-visible:shadow-edge-accent";
  const fieldLabel = "mb-1.5 block text-xs text-slate-400";

  return (
    <div className="mx-auto flex w-full max-w-[1060px] flex-col gap-[18px] px-5 pb-11 pt-7 sm:px-8">
      {/* Header & Tabs */}
      <div>
        <h1 className="text-[25px] font-medium leading-tight text-slate-100">{t("admin.securityTitle")}</h1>
        <p className="mt-1.5 text-[13.5px] text-slate-400">{t("admin.securitySubtitle")}</p>
      </div>

      {/* Tab Selector Controls */}
      <div className="lm-tabstrip self-start">
        {securityTabs.map((tab) => {
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

      {/* TAB 1: READINESS SCORECARD */}
      {activeTab === "readiness" && (
        <div className="flex flex-col gap-3.5">
          {/* The score is counted from the checks that came back. It used to
              read a hardcoded "10 / 10" with an "all checks passed" badge
              regardless of what the preflight endpoint reported - including
              when it reported failures, or nothing at all. */}
          <div className="flex flex-wrap items-center justify-between gap-4 rounded-2xl bg-dark-panel p-[18px] shadow-edge">
            <div className="min-w-[260px] flex-1">
              <h2 className="flex items-center gap-2 text-[17px] font-medium leading-tight text-slate-100">
                <Shield className="h-[17px] w-[17px] flex-none text-indigo-500" />
                {t("admin.preflightTitle")}
              </h2>
              <p className="mt-1 text-[13px] leading-relaxed text-slate-400">{t("admin.readinessIntro")}</p>
            </div>
            {readiness && readiness.length > 0 ? (
              (() => {
                const passed = readiness.filter((c) => c.status === "PASSED").length;
                const total = readiness.length;
                return (
                  <div className="flex flex-none items-center gap-3">
                    <span className="font-mono text-[30px] font-medium leading-none tabular-nums text-slate-100">
                      {passed} / {total}
                    </span>
                    <Badge variant={passed === total ? "success" : "warning"}>
                      {passed === total ? t("ui.allChecksPassed") : t("admin.checksPassed", { passed, total })}
                    </Badge>
                  </div>
                );
              })()
            ) : (
              <span className="text-[13px] text-slate-400">{t("admin.noChecks")}</span>
            )}
          </div>

          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            {(readiness ?? []).map((chk) => (
              <div key={chk.id} className="flex flex-col gap-2 rounded-2xl bg-dark-panel p-4 shadow-edge">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <span className="min-w-0 text-[13.5px] text-slate-100">
                    #{chk.id}: {chk.name}
                  </span>
                  <Badge
                    variant={chk.status === "PASSED" ? "success" : chk.status === "FAILED" ? "danger" : "warning"}
                    className="flex-none"
                  >
                    {chk.status}
                  </Badge>
                </div>
                <div className="break-words text-[12.5px] leading-relaxed text-slate-400">{chk.details}</div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* TAB 2: TLS - read from the protocols service cert watcher */}
      {activeTab === "tls" && (
        <section className={panel}>
          <h2 className="flex items-center gap-2 text-[17px] font-medium leading-tight text-slate-100">
            <Lock className="h-[17px] w-[17px] flex-none text-indigo-500" />
            {t("admin.tlsTitle")}
          </h2>

          {/* A self-signed certificate on the mail ports is refused by every
              client that verifies, so it is stated outright rather than left
              to be inferred from a status word. */}
          {tlsStatus?.self_signed ? (
            <div className="flex items-start gap-2.5 rounded-xl bg-amber-500/10 px-3.5 py-3 shadow-edge">
              <AlertTriangle className="mt-px h-4 w-4 flex-none text-amber-400" />
              <span className="text-[12.5px] leading-relaxed text-amber-200">
                {t("admin.tlsSelfSigned", { issuer: String(tlsStatus.issuer ?? "-") })}
              </span>
            </div>
          ) : null}

          {tlsStatus ? (
            <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
              <div className="flex flex-col gap-1 rounded-xl bg-dark-card p-3.5 shadow-edge">
                <div className="text-xs text-slate-400">{t("admin.certificateState")}</div>
                <div
                  className={`text-[15px] font-medium ${
                    tlsStatus.state === "OK" ? "text-indigo-400" : "text-amber-400"
                  }`}
                >
                  {String(tlsStatus.state ?? "UNKNOWN")}
                </div>
                <div className="text-[11.5px] text-slate-500">
                  {typeof tlsStatus.expires_in_days === "number"
                    ? t("admin.expiresInDays", { days: tlsStatus.expires_in_days })
                    : t("admin.noCertificate")}
                </div>
              </div>

              <div className="flex flex-col gap-1 rounded-xl bg-dark-card p-3.5 shadow-edge">
                <div className="text-xs text-slate-400">{t("admin.mailHost")}</div>
                <div className="break-all text-[15px] font-medium text-slate-100">
                  {String(tlsStatus.mail_host ?? "-")}
                </div>
                <div className="text-[11.5px] text-slate-500">
                  {t("admin.tlsMode")}: {String(tlsStatus.tls_mode ?? "-")}
                </div>
              </div>

              <div className="flex flex-col gap-1 rounded-xl bg-dark-card p-3.5 shadow-edge">
                <div className="text-xs text-slate-400">{t("admin.watcherHealthy")}</div>
                <div
                  className={`text-[15px] font-medium ${
                    tlsStatus.watcher_healthy ? "text-indigo-400" : "text-amber-400"
                  }`}
                >
                  {tlsStatus.watcher_healthy ? t("ui.healthy") : t("common.unavailable")}
                </div>
                <div className="text-[11.5px] text-slate-500">
                  {t("admin.lastReload")}:{" "}
                  {tlsStatus.last_reload
                    ? new Date(String(tlsStatus.last_reload)).toLocaleString()
                    : t("common.never")}
                </div>
              </div>
            </div>
          ) : (
            <div className="p-6 text-center text-[13px] text-slate-400">{t("errors.loadFailed")}</div>
          )}
        </section>
      )}

      {/* TAB 3: RSPAMD ANTI-SPAM THRESHOLDS */}
      {activeTab === "rspamd" && (
        <section className={`${panel} max-w-[620px]`}>
          <div>
            <h2 className="flex items-center gap-2 text-[17px] font-medium leading-tight text-slate-100">
              <Sliders className="h-[17px] w-[17px] flex-none text-indigo-500" />
              {t("admin.tabRspamd")}
            </h2>
            <p className="mt-1 text-[13px] leading-relaxed text-slate-400">{t("admin.rspamdIntro")}</p>
          </div>

          {rspamdSaved && (
            <div className="flex items-center gap-2 rounded-xl bg-dark-card px-3.5 py-3 text-[12.5px] text-slate-200 shadow-edge">
              <CheckCircle2 className="h-4 w-4 flex-none text-indigo-500" />
              <span>{t("admin.rspamdSaved")}</span>
            </div>
          )}

          <form onSubmit={handleSaveRspamd} className="flex flex-col gap-3">
            <div>
              <label htmlFor="greylist-score" className={fieldLabel}>
                {t("admin.greylistScore")}
              </label>
              <input
                id="greylist-score"
                type="number"
                step="0.5"
                value={greylistScore}
                onChange={(e) => setGreylistScore(parseFloat(e.target.value))}
                className={`${input} font-mono`}
              />
            </div>

            <div>
              <label htmlFor="header-score" className={fieldLabel}>
                {t("admin.headerScore")}
              </label>
              <input
                id="header-score"
                type="number"
                step="0.5"
                value={addHeaderScore}
                onChange={(e) => setAddHeaderScore(parseFloat(e.target.value))}
                className={`${input} font-mono`}
              />
            </div>

            <div>
              <label htmlFor="reject-score" className={fieldLabel}>
                {t("admin.rejectScore")}
              </label>
              <input
                id="reject-score"
                type="number"
                step="0.5"
                value={rejectScore}
                onChange={(e) => setRejectScore(parseFloat(e.target.value))}
                className={`${input} font-mono`}
              />
            </div>

            <Button type="submit" variant="primary" size="md" className="self-start">
              {t("admin.saveThresholds")}
            </Button>
          </form>
        </section>
      )}

      {/* TAB 4: AUDIT LOG & QUEUE ID TRANSACTION TRACE */}
      {activeTab === "audit" && (
        <div className="flex flex-col gap-3.5">
          {/* Queue ID Search Box */}
          <section className={panel}>
            <div>
              <h2 className="flex items-center gap-2 text-[17px] font-medium leading-tight text-slate-100">
                <Search className="h-[17px] w-[17px] flex-none text-indigo-500" />
                {t("admin.traceTitle")}
              </h2>
              <p className="mt-1 text-[13px] leading-relaxed text-slate-400">{t("admin.tracePlaceholder")}</p>
            </div>

            <form onSubmit={handleSearchQueueId} className="flex flex-wrap gap-2">
              <input
                type="text"
                value={searchQueueId}
                onChange={(e) => setSearchQueueId(e.target.value)}
                placeholder={t("admin.tracePlaceholder")}
                aria-label={t("admin.traceTitle")}
                required
                className={`${input} min-w-[240px] flex-1 font-mono`}
              />
              <Button type="submit" variant="primary" size="md" className="flex-none">
                {t("admin.traceTitle")}
              </Button>
            </form>

            {tracedSteps && (
              <div className="flex flex-col gap-2">
                {/* These two lines were literal Portuguese, so an interface
                    running in English still said "Resultado do Rastreamento"
                    and "Etapa". */}
                <h3 className="break-all text-[13.5px] text-slate-100">
                  {t("admin.traceResultFor", { id: searchQueueId })}
                </h3>
                {tracedSteps.map((st) => (
                  <div
                    key={st.step}
                    className="flex flex-wrap items-center gap-3 rounded-xl bg-dark-card px-3.5 py-3 shadow-edge"
                  >
                    <div className="min-w-0 flex-1">
                      <div className="break-words text-[13.5px] text-slate-100">
                        {t("admin.traceStep", { n: st.step })}: {st.title}
                      </div>
                      <div className="mt-0.5 break-words text-[11.5px] text-slate-400">{st.detail}</div>
                    </div>
                    <Badge variant="info" className="flex-none">
                      {st.status}
                    </Badge>
                  </div>
                ))}
              </div>
            )}
          </section>

          {/* Immutable System Audit Log Table */}
          <div className="rounded-2xl bg-dark-panel px-[18px] pb-3 pt-1.5 shadow-edge">
            <div className="overflow-x-auto">
              <table className="lm-table">
                <thead>
                  <tr>
                    <th className="pl-0">{t("ui.timestamp")}</th>
                    <th>{t("ui.jobId")}</th>
                    <th>{t("ui.actor")}</th>
                    <th>{t("ui.eventAction")}</th>
                    <th>{t("ui.targetObject")}</th>
                    <th className="pr-0 text-right">{t("ui.clientIp")}</th>
                  </tr>
                </thead>
                <tbody>
                  {auditEntries.map((aud) => (
                    <tr key={aud.id}>
                      <td className="whitespace-nowrap pl-0 text-[12.5px] text-slate-400">{aud.timestamp}</td>
                      <td className="font-mono text-[12.5px] text-slate-200">{aud.queueId}</td>
                      <td className="break-words text-[13px] text-slate-100">{aud.actor}</td>
                      <td>
                        <Badge variant="info" className="whitespace-nowrap">
                          {aud.action}
                        </Badge>
                      </td>
                      <td className="break-words text-[13px] text-slate-300">{aud.target}</td>
                      <td className="pr-0 text-right font-mono text-[12.5px] text-slate-500">{aud.ip}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            {auditEntries.length === 0 && (
              <div className="p-8 text-center text-[13px] text-slate-400">{t("ui.noData")}</div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
