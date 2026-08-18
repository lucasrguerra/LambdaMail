"use client";

import React, { useEffect, useState } from "react";
import { useTranslations } from "../../../../i18n/provider";
import { Badge } from "../../../../components/ui/Badge";

interface DmarcSource {
  ip: string;
  org: string;
  count: number;
  spf: string;
  dkim: string;
}

interface DmarcData {
  total_messages: number;
  spf_pass_count: number;
  dkim_pass_count: number;
  dmarc_pass_count: number;
  sources: DmarcSource[];
}

export default function AdminDmarcPage() {
  const t = useTranslations();
  const [data, setData] = useState<DmarcData | null>(null);

  useEffect(() => {
    fetch("/api/v1/admin/dmarc")
      .then((res) => (res.ok ? res.json() : null))
      .then((d) => setData(d))
      .catch(() => {});
  }, []);

  const pct = (part: number) =>
    data && data.total_messages > 0 ? `${Math.round((part / data.total_messages) * 100)}%` : t("ui.noData");

  const tiles = [
    { label: t("ui.totalEvaluated"), value: data ? String(data.total_messages) : t("ui.noData") },
    { label: t("ui.spfRatio"), value: data ? pct(data.spf_pass_count) : t("ui.noData") },
    { label: t("ui.dkimAlignment"), value: data ? pct(data.dkim_pass_count) : t("ui.noData") },
    /* This tile printed the literal string "quarantine" whatever the domains
       actually publish - the report endpoint returns no policy at all, so it
       was asserting an enforcement level nobody had configured. It reports the
       DMARC pass ratio it genuinely has instead. */
    { label: t("ui.dmarcEnforcement"), value: data ? pct(data.dmarc_pass_count) : t("ui.noData") },
  ];

  return (
    <div className="mx-auto flex w-full max-w-[1060px] flex-col gap-[18px] px-5 pb-11 pt-7 sm:px-8">
      <div>
        <h1 className="text-[25px] font-medium leading-tight text-slate-100">{t("admin.dmarcTitle")}</h1>
        <p className="mt-1.5 text-[13.5px] text-slate-400">{t("admin.dmarcSubtitle")}</p>
      </div>

      {/* DMARC Stats Summary Grid */}
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
        {tiles.map((tile) => (
          <div key={tile.label} className="flex flex-col gap-2.5 rounded-2xl bg-dark-panel p-4 shadow-edge">
            <div className="text-[12.5px] leading-snug text-slate-400">{tile.label}</div>
            <div className="text-[30px] font-medium leading-none tracking-[-0.02em] tabular-nums text-slate-100">
              {tile.value}
            </div>
          </div>
        ))}
      </div>

      {/* Sending Sources Table */}
      <div className="rounded-2xl bg-dark-panel px-[18px] pb-3 pt-1.5 shadow-edge">
        <div className="overflow-x-auto">
          <table className="lm-table">
            <thead>
              <tr>
                <th className="pl-0">{t("ui.sourceIp")}</th>
                <th>{t("ui.reportingOrg")}</th>
                <th>{t("ui.messageVolume")}</th>
                <th>{t("ui.spfResult")}</th>
                <th className="pr-0 text-right">{t("ui.dkimResult")}</th>
              </tr>
            </thead>
            <tbody>
              {(data?.sources || []).map((src, idx) => (
                <tr key={idx}>
                  <td className="pl-0 font-mono text-[12.5px] text-slate-100">{src.ip}</td>
                  <td className="break-words text-[13px] text-slate-300">{src.org}</td>
                  <td className="text-[13px] tabular-nums text-slate-400">{src.count}</td>
                  <td>
                    {/* These were "badge-verified" and "badge-danger", class
                        names no stylesheet defines, so both verdicts rendered
                        as identical unstyled text. */}
                    <Badge variant={src.spf === "pass" ? "success" : "danger"}>{src.spf.toUpperCase()}</Badge>
                  </td>
                  <td className="pr-0 text-right">
                    <Badge variant={src.dkim === "pass" ? "success" : "danger"}>{src.dkim.toUpperCase()}</Badge>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {(data?.sources ?? []).length === 0 && (
          <div className="p-8 text-center text-[13px] text-slate-400">{t("ui.noData")}</div>
        )}
      </div>
    </div>
  );
}
