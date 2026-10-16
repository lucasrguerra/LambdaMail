"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Upload, ZoomIn, ZoomOut, Download, Trash2, CheckCircle2, AlertTriangle, Copy, Play, CloudUpload } from "lucide-react";
import { Badge } from "./ui/Badge";
import { Button } from "./ui/Button";
import { useTranslations } from "../i18n/provider";
import {
  BimiLogoError,
  DEFAULT_LAYOUT,
  MAX_LOGO_BYTES,
  TRACE_PRESETS,
  buildLogoSvg,
  byteLength,
  clampLayout,
  isHexColor,
  sanitizeSvg,
  traceRaster,
  type Artwork,
  type Layout,
  type Raster,
  type TracePreset,
} from "../lib/bimiLogo";

/** The frame the logo is positioned in. Square, like the file it produces. */
const VIEWPORT = 288;
/** Bitmaps are traced at this size at most: tracing cost grows with the pixels. */
const TRACE_MAX_SIDE = 512;

type Mask = "circle" | "rounded" | "none";

interface CertificateInfo {
  present: boolean;
  valid?: boolean;
  violations?: string[];
  subject?: string;
  organization?: string;
  issuer?: string;
  not_after?: string;
  dns_names?: string[];
  kind?: string;
  matches_logo?: boolean;
  warnings?: string[];
}

interface BimiState {
  configured: boolean;
  svg?: string;
  vmc_url?: string;
  logo_url?: string;
  certificate_url?: string;
  certificate?: CertificateInfo;
  /** The TXT record as the reconciler writes it. */
  dns_record?: string;
  /** Why a certificate that is held is left out of that record, if it is. */
  certificate_withheld?: string;
  /** Whether saving a change publishes the record by itself. */
  dns_publishing?: boolean;
}

interface PublishOutcome {
  ok: boolean;
  message: string;
}

interface CheckResult {
  id: string;
  status: "ok" | "warn" | "fail";
  detail: string;
}

interface BimiPanelProps {
  domain: string;
  onNotice: (message: string) => void;
}

const panelClass = "flex flex-col gap-4 rounded-2xl bg-dark-panel p-[18px] shadow-edge";
const inputClass =
  "w-full min-h-[36px] rounded-[10px] bg-dark-card px-3 py-2 text-[13.5px] text-slate-100 placeholder-slate-500 shadow-edge transition-shadow focus:outline-none focus-visible:shadow-edge-accent";
const labelClass = "mb-1.5 block text-xs text-slate-400";
const subPanel = "flex flex-col gap-3 rounded-xl bg-dark-card/60 p-4 shadow-edge";

function dataUrl(svg: string): string {
  return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
}

function maskClass(mask: Mask): string {
  if (mask === "circle") return "rounded-full";
  if (mask === "rounded") return "rounded-[22%]";
  return "";
}

function formatBytes(n: number): string {
  return `${(n / 1024).toFixed(1)} KB`;
}

/** Reads a bitmap file into pixels, scaled down so tracing stays quick. */
async function readRaster(file: File): Promise<Raster> {
  const url = URL.createObjectURL(file);
  try {
    const img = await new Promise<HTMLImageElement>((resolve, reject) => {
      const el = new Image();
      el.onload = () => resolve(el);
      el.onerror = () => reject(new BimiLogoError("empty"));
      el.src = url;
    });
    const ratio = Math.min(1, TRACE_MAX_SIDE / Math.max(img.naturalWidth, img.naturalHeight));
    const width = Math.max(1, Math.round(img.naturalWidth * ratio));
    const height = Math.max(1, Math.round(img.naturalHeight * ratio));
    const canvas = document.createElement("canvas");
    canvas.width = width;
    canvas.height = height;
    const ctx = canvas.getContext("2d", { willReadFrequently: true });
    if (!ctx) throw new BimiLogoError("empty");
    ctx.drawImage(img, 0, 0, width, height);
    const { data } = ctx.getImageData(0, 0, width, height);
    return { width, height, data };
  } finally {
    URL.revokeObjectURL(url);
  }
}

export function BimiPanel({ domain, onNotice }: BimiPanelProps) {
  const t = useTranslations();

  const [bimi, setBimi] = useState<BimiState | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // The studio: what was chosen, and how it is placed
  const [art, setArt] = useState<Artwork | null>(null);
  const [raster, setRaster] = useState<Raster | null>(null);
  const [preset, setPreset] = useState<TracePreset>("balanced");
  const [loweredAuto, setLoweredAuto] = useState(false);
  const [layout, setLayout] = useState<Layout>(DEFAULT_LAYOUT);
  const [background, setBackground] = useState("#ffffff");
  const [title, setTitle] = useState(domain);
  const [mask, setMask] = useState<Mask>("circle");
  const [problems, setProblems] = useState<string[]>([]);

  const [vmcUrl, setVmcUrl] = useState("");
  const [pem, setPem] = useState("");
  const [certProblems, setCertProblems] = useState<string[]>([]);

  const [publishing, setPublishing] = useState(false);
  const [published, setPublished] = useState<PublishOutcome | null>(null);

  const [checks, setChecks] = useState<{ ready: boolean; checks: CheckResult[] } | null>(null);
  const [checking, setChecking] = useState(false);
  const [copied, setCopied] = useState(false);

  const fileRef = useRef<HTMLInputElement | null>(null);
  const pemRef = useRef<HTMLInputElement | null>(null);
  const dragRef = useRef<{ x: number; y: number; from: Layout } | null>(null);

  const base = `/api/v1/admin/bimi`;
  const q = `domain=${encodeURIComponent(domain)}`;

  const load = useCallback(async () => {
    try {
      const res = await fetch(`${base}?${q}`);
      const data: BimiState = res.ok ? await res.json() : { configured: false };
      setBimi(data);
      setVmcUrl(data.vmc_url ?? "");
    } catch {
      setBimi({ configured: false });
    }
  }, [base, q]);

  // The mark belongs to the domain: a different domain starts from scratch
  useEffect(() => {
    setArt(null);
    setRaster(null);
    setProblems([]);
    setError(null);
    setChecks(null);
    setPublished(null);
    setPem("");
    setCertProblems([]);
    setTitle(domain);
    setBimi(null);
    void load();
  }, [domain, load]);

  // ------------------------------------------------------------ the studio

  /** Traces pixels, walking down the presets until the file fits the limit. */
  const traceFitting = useCallback(
    (pixels: Raster, from: TracePreset, current: { layout: Layout; title: string; background: string }) => {
      const tracer = import("imagetracerjs").then((m) => m.default);
      return tracer.then((engine) => {
        const start = TRACE_PRESETS.indexOf(from);
        for (let i = start; i < TRACE_PRESETS.length; i++) {
          const traced = traceRaster(pixels, TRACE_PRESETS[i], engine);
          const size = byteLength(buildLogoSvg(traced, current.layout, current.title, current.background));
          if (size <= MAX_LOGO_BYTES || i === TRACE_PRESETS.length - 1) {
            return { art: traced, preset: TRACE_PRESETS[i], lowered: i > start };
          }
        }
        throw new BimiLogoError("tooComplex");
      });
    },
    [],
  );

  const openFile = async (file: File) => {
    setError(null);
    setProblems([]);
    setBusy(true);
    try {
      setLayout(DEFAULT_LAYOUT);
      if (file.type === "image/svg+xml" || /\.svg$/i.test(file.name)) {
        setRaster(null);
        setLoweredAuto(false);
        setArt(sanitizeSvg(await file.text()));
      } else if (/^image\/(png|jpeg|webp)$/.test(file.type)) {
        const pixels = await readRaster(file);
        const result = await traceFitting(pixels, "balanced", { layout: DEFAULT_LAYOUT, title, background });
        setRaster(pixels);
        setArt(result.art);
        setPreset(result.preset);
        setLoweredAuto(result.lowered);
      } else {
        throw new BimiLogoError("empty");
      }
    } catch (err) {
      setArt(null);
      const code = err instanceof BimiLogoError ? err.code : "notImage";
      setError(t(`admin.bimiError_${code}`));
    } finally {
      setBusy(false);
    }
  };

  const changePreset = async (next: TracePreset) => {
    if (!raster) return;
    setPreset(next);
    setLoweredAuto(false);
    setBusy(true);
    try {
      const engine = (await import("imagetracerjs")).default;
      setArt(traceRaster(raster, next, engine));
    } catch {
      setError(t("admin.bimiError_empty"));
    } finally {
      setBusy(false);
    }
  };

  const finalSvg = useMemo(() => (art ? buildLogoSvg(art, layout, title, background) : ""), [art, layout, title, background]);
  const size = finalSvg ? byteLength(finalSvg) : 0;
  const tooBig = size > MAX_LOGO_BYTES;

  const onPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!art) return;
    e.currentTarget.setPointerCapture(e.pointerId);
    dragRef.current = { x: e.clientX, y: e.clientY, from: layout };
  };
  const onPointerMove = (e: React.PointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current;
    if (!drag) return;
    setLayout(
      clampLayout({
        zoom: drag.from.zoom,
        dx: drag.from.dx + (e.clientX - drag.x) / VIEWPORT,
        dy: drag.from.dy + (e.clientY - drag.y) / VIEWPORT,
      }),
    );
  };
  const endDrag = () => {
    dragRef.current = null;
  };

  const download = () => {
    const url = URL.createObjectURL(new Blob([finalSvg], { type: "image/svg+xml" }));
    const a = document.createElement("a");
    a.href = url;
    a.download = `${domain}-bimi.svg`;
    a.click();
    URL.revokeObjectURL(url);
  };

  const saveLogo = async () => {
    if (!finalSvg || tooBig) return;
    setBusy(true);
    setProblems([]);
    setError(null);
    try {
      const res = await fetch(`${base}?${q}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ svg: finalSvg, vmc_url: vmcUrl.trim() }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) {
        // Every rule that failed, not the first: a receiver that rejects the
        // mark shows nothing at all, so one round trip per rule is the worst
        // possible way to learn this format.
        setProblems(data.violations ?? [data.message ?? t("errors.serverError")]);
        return;
      }
      setArt(null);
      setRaster(null);
      onNotice(t("admin.bimiSaved"));
      await load();
    } catch {
      setError(t("errors.serverError"));
    } finally {
      setBusy(false);
    }
  };

  const saveVmcUrl = async () => {
    if (!bimi?.svg) return;
    setBusy(true);
    try {
      await fetch(`${base}?${q}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ svg: bimi.svg, vmc_url: vmcUrl.trim() }),
      });
      await load();
    } finally {
      setBusy(false);
    }
  };

  const removeLogo = async () => {
    setBusy(true);
    try {
      await fetch(`${base}?${q}`, { method: "DELETE" });
      setChecks(null);
      await load();
    } finally {
      setBusy(false);
    }
  };

  // --------------------------------------------------------- certificate

  const sendPem = async (text: string) => {
    if (!text.trim()) return;
    setBusy(true);
    setCertProblems([]);
    try {
      const res = await fetch(`${base}/certificate?${q}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ pem: text }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) {
        setCertProblems(data.violations ?? [data.message ?? t("errors.serverError")]);
        return;
      }
      setPem("");
      onNotice(t("admin.bimiCertSaved"));
      await load();
    } catch {
      setCertProblems([t("errors.serverError")]);
    } finally {
      setBusy(false);
    }
  };

  const removeCertificate = async () => {
    setBusy(true);
    try {
      await fetch(`${base}/certificate?${q}`, { method: "DELETE" });
      setChecks(null);
      await load();
    } finally {
      setBusy(false);
    }
  };

  // -------------------------------------------------------------- publish

  /** Writes the domain's records to the DNS provider now, the same sync that runs on a timer. */
  const publishNow = async () => {
    setPublishing(true);
    setPublished(null);
    try {
      const res = await fetch(`/api/v1/admin/dns/reconcile?${q}`, { method: "POST" });
      const data = await res.json().catch(() => ({}));
      if (res.status === 503) {
        setPublished({ ok: false, message: t("admin.bimiPublishUnavailable") });
      } else if (!res.ok) {
        setPublished({ ok: false, message: data.message ?? t("errors.serverError") });
      } else {
        const problems = [...(data.errors ?? []), ...(data.conflicts ?? [])];
        setPublished({
          ok: problems.length === 0,
          message:
            t("admin.bimiPublished", { created: data.created ?? 0, updated: data.updated ?? 0, deleted: data.deleted ?? 0 }) +
            (problems.length ? ` ${problems.join("; ")}` : ""),
        });
        setChecks(null);
      }
    } catch {
      setPublished({ ok: false, message: t("errors.serverError") });
    } finally {
      setPublishing(false);
    }
  };

  // --------------------------------------------------------------- check

  const runCheck = async () => {
    setChecking(true);
    try {
      const res = await fetch(`${base}/check?${q}`);
      const data = await res.json();
      setChecks(res.ok ? { ready: Boolean(data.ready), checks: data.checks ?? [] } : null);
    } catch {
      setChecks(null);
    } finally {
      setChecking(false);
    }
  };

  const record = bimi?.dns_record ?? "";

  const copyRecord = async () => {
    try {
      await navigator.clipboard.writeText(record);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* the text is selectable on screen */
    }
  };

  const cert = bimi?.certificate;
  const hasCertificate = Boolean(cert?.present);
  const noVmc = bimi?.configured && !hasCertificate && !vmcUrl.trim();

  const statusBadge = (status: CheckResult["status"]) =>
    status === "ok" ? "success" : status === "warn" ? "warning" : "danger";

  return (
    <section className={panelClass}>
      <div>
        <h2 className="text-[17px] font-medium leading-tight text-slate-100">{t("admin.bimiTitle", { domain })}</h2>
        <p className="mt-1 text-[12.5px] leading-relaxed text-slate-400">{t("admin.bimiExplainer")}</p>
      </div>

      <div className="flex flex-wrap items-center gap-2.5">
        <Badge variant={bimi?.configured ? "success" : "neutral"}>
          {bimi?.configured ? t("admin.bimiConfigured") : t("admin.bimiNotConfigured")}
        </Badge>
        {noVmc && <span className="text-[11.5px] leading-relaxed text-amber-400">{t("admin.bimiNoVmc")}</span>}
      </div>

      {/* ---------------------------------------------------------- logo */}
      <div className={subPanel}>
        {!art ? (
          <div className="flex flex-wrap items-center gap-4">
            <span className="flex h-[88px] w-[88px] flex-none items-center justify-center overflow-hidden rounded-full bg-dark-card shadow-edge">
              {bimi?.svg ? (
                // eslint-disable-next-line @next/next/no-img-element
                <img src={dataUrl(bimi.svg)} alt="" className="h-full w-full object-cover" />
              ) : (
                <span className="text-[11px] text-slate-500">{t("admin.bimiNone")}</span>
              )}
            </span>
            <div className="flex min-w-[220px] flex-1 flex-col gap-2">
              <p className="text-[12px] leading-relaxed text-slate-400">{t("admin.bimiFormatHint")}</p>
              <div className="flex flex-wrap items-center gap-2">
                <Button variant="secondary" size="sm" onClick={() => fileRef.current?.click()} disabled={busy}>
                  <Upload className="h-3.5 w-3.5" />
                  <span>{bimi?.configured ? t("admin.bimiReplace") : t("admin.bimiChoose")}</span>
                </Button>
                {bimi?.configured && (
                  <Button variant="ghost" size="sm" onClick={() => void removeLogo()} disabled={busy}>
                    <Trash2 className="h-3.5 w-3.5" />
                    <span>{t("common.delete")}</span>
                  </Button>
                )}
              </div>
            </div>
          </div>
        ) : (
          <div className="flex flex-wrap items-start gap-6">
            <div className="flex flex-col items-center gap-3">
              <div
                role="img"
                aria-label={t("admin.bimiDragHint")}
                onPointerDown={onPointerDown}
                onPointerMove={onPointerMove}
                onPointerUp={endDrag}
                onPointerCancel={endDrag}
                className={`relative cursor-grab touch-none overflow-hidden shadow-edge active:cursor-grabbing ${maskClass(mask)}`}
                style={{ width: VIEWPORT, height: VIEWPORT, background: "repeating-conic-gradient(#1e293b 0% 25%, #0f172a 0% 50%) 0 0 / 16px 16px" }}
              >
                {/* eslint-disable-next-line @next/next/no-img-element */}
                <img src={dataUrl(finalSvg)} alt="" draggable={false} className="pointer-events-none h-full w-full select-none" />
              </div>

              <div className="flex w-full max-w-[288px] items-center gap-2.5">
                <ZoomOut className="h-4 w-4 flex-none text-slate-400" />
                <input
                  type="range"
                  min={0.2}
                  max={4}
                  step={0.01}
                  value={layout.zoom}
                  onChange={(e) => setLayout((l) => clampLayout({ ...l, zoom: Number(e.target.value) }))}
                  aria-label={t("admin.bimiZoom")}
                  className="h-1 flex-1 cursor-pointer appearance-none rounded-full bg-dark-card accent-indigo-500"
                />
                <ZoomIn className="h-4 w-4 flex-none text-slate-400" />
              </div>

              <div className="flex flex-wrap items-center justify-center gap-1.5" role="group" aria-label={t("admin.bimiMask")}>
                {(["circle", "rounded", "none"] as Mask[]).map((m) => (
                  <button
                    key={m}
                    type="button"
                    onClick={() => setMask(m)}
                    aria-pressed={mask === m}
                    className={`rounded-md px-2.5 py-1 text-[11px] ${mask === m ? "bg-indigo-800 text-indigo-100" : "bg-slate-800 text-slate-300"}`}
                  >
                    {t(`admin.bimiMask_${m}`)}
                  </button>
                ))}
              </div>
              <p className="max-w-[288px] text-center text-[11.5px] text-slate-500">{t("admin.bimiDragHint")}</p>
            </div>

            <div className="flex min-w-[240px] flex-1 flex-col gap-3">
              <div>
                <label htmlFor="bimi-title" className={labelClass}>{t("admin.bimiBrandTitle")}</label>
                <input id="bimi-title" value={title} onChange={(e) => setTitle(e.target.value)} maxLength={100} className={inputClass} />
              </div>

              <div>
                <label htmlFor="bimi-bg" className={labelClass}>{t("admin.bimiBackground")}</label>
                <div className="flex items-center gap-2">
                  <input
                    id="bimi-bg"
                    type="color"
                    value={isHexColor(background) ? background : "#ffffff"}
                    onChange={(e) => setBackground(e.target.value)}
                    className="h-9 w-12 cursor-pointer rounded-md bg-dark-card p-1 shadow-edge"
                  />
                  <span className="font-mono text-[12px] text-slate-400">{background}</span>
                </div>
              </div>

              {raster && (
                <div>
                  <label htmlFor="bimi-preset" className={labelClass}>{t("admin.bimiPreset")}</label>
                  <select
                    id="bimi-preset"
                    value={preset}
                    onChange={(e) => void changePreset(e.target.value as TracePreset)}
                    disabled={busy}
                    className={inputClass}
                  >
                    {TRACE_PRESETS.map((p) => (
                      <option key={p} value={p}>{t(`admin.bimiPreset_${p}`)}</option>
                    ))}
                  </select>
                  <p className="mt-1.5 text-[11.5px] leading-relaxed text-slate-500">{t("admin.bimiTraced")}</p>
                  {loweredAuto && <p className="mt-1 text-[11.5px] leading-relaxed text-amber-400">{t("admin.bimiAutoLowered")}</p>}
                </div>
              )}

              <div className={`text-[12.5px] ${tooBig ? "text-rose-300" : "text-slate-400"}`}>
                {t("admin.bimiSize", { size: formatBytes(size), max: formatBytes(MAX_LOGO_BYTES) })}
                {tooBig && <span className="mt-1 block text-[11.5px]">{t("admin.bimiTooBig")}</span>}
              </div>

              {art.removed.length > 0 && (
                <p className="text-[11.5px] leading-relaxed text-amber-400">{t("admin.bimiRemoved", { items: art.removed.join(", ") })}</p>
              )}
              {art.warnings.includes("text") && (
                <p className="text-[11.5px] leading-relaxed text-amber-400">{t("admin.bimiTextWarning")}</p>
              )}

              <div className="flex flex-wrap items-center gap-2">
                <Button variant="secondary" size="sm" onClick={() => { setArt(null); setRaster(null); }} disabled={busy}>
                  {t("common.cancel")}
                </Button>
                <Button variant="secondary" size="sm" onClick={download}>
                  <Download className="h-3.5 w-3.5" />
                  <span>{t("admin.bimiDownload")}</span>
                </Button>
                <Button variant="primary" size="sm" onClick={() => void saveLogo()} disabled={busy || tooBig}>
                  {busy ? t("common.loading") : t("admin.bimiSaveLogo")}
                </Button>
              </div>
            </div>
          </div>
        )}

        {error && <div className="rounded-xl bg-rose-900/60 px-3.5 py-2.5 text-[12.5px] text-rose-200 shadow-edge">{error}</div>}
        {problems.length > 0 && (
          <div className="flex flex-col gap-1 rounded-xl bg-rose-900/50 px-3.5 py-3 shadow-edge">
            <span className="text-[12.5px] text-rose-100">{t("admin.bimiRejected")}</span>
            {problems.map((p) => (
              <span key={p} className="font-mono text-[11.5px] leading-relaxed text-rose-200">{p}</span>
            ))}
          </div>
        )}

        <input
          ref={fileRef}
          type="file"
          accept="image/svg+xml,image/png,image/jpeg,image/webp,.svg"
          className="hidden"
          onChange={(e) => {
            const file = e.target.files?.[0];
            if (file) void openFile(file);
            // Cleared so choosing the same file twice in a row still fires
            e.target.value = "";
          }}
        />
      </div>

      {/* ---------------------------------------------------- certificate */}
      {bimi?.configured && (
        <div className={subPanel}>
          <h3 className="text-[14px] font-medium text-slate-100">{t("admin.bimiCertTitle")}</h3>
          <p className="text-[12px] leading-relaxed text-slate-400">{t("admin.bimiCertExplainer")}</p>

          {hasCertificate && cert ? (
            <div className="flex flex-col gap-2 rounded-lg bg-dark-panel px-3.5 py-3 shadow-edge">
              <div className="flex flex-wrap items-center gap-2">
                <Badge variant={cert.valid ? "success" : "danger"}>
                  {cert.valid ? t("admin.bimiCertValid") : t("admin.bimiCertInvalid")}
                </Badge>
                {cert.kind && cert.kind !== "unknown" && <Badge variant="info">{cert.kind.toUpperCase()}</Badge>}
              </div>
              {cert.organization && (
                <span className="text-[12.5px] text-slate-200">
                  {t("admin.bimiCertIssued", { organization: cert.organization, issuer: cert.issuer ?? "" })}
                </span>
              )}
              {cert.not_after && (
                <span className="text-[12px] text-slate-400">
                  {t("admin.bimiCertValidUntil", { date: new Date(cert.not_after).toLocaleDateString() })}
                </span>
              )}
              {cert.dns_names && cert.dns_names.length > 0 && (
                <span className="break-all text-[11.5px] text-slate-500">{t("admin.bimiCertDomains", { names: cert.dns_names.join(", ") })}</span>
              )}
              <span className={`text-[12px] ${cert.matches_logo ? "text-slate-300" : "text-rose-300"}`}>
                {cert.matches_logo ? t("admin.bimiCertMatches") : t("admin.bimiCertMismatch")}
              </span>
              {(cert.violations ?? []).map((v) => (
                <span key={v} className="font-mono text-[11.5px] leading-relaxed text-rose-200">{v}</span>
              ))}
              {(cert.warnings ?? []).map((w) => (
                <span key={w} className="text-[11.5px] leading-relaxed text-amber-400">{w}</span>
              ))}
              <span className="break-all font-mono text-[11px] text-slate-500">{bimi.certificate_url}</span>
            </div>
          ) : null}

          <div className="flex flex-wrap items-center gap-2">
            <Button variant="secondary" size="sm" onClick={() => pemRef.current?.click()} disabled={busy}>
              <Upload className="h-3.5 w-3.5" />
              <span>{hasCertificate ? t("admin.bimiCertReplace") : t("admin.bimiCertChoose")}</span>
            </Button>
            {hasCertificate && (
              <Button variant="ghost" size="sm" onClick={() => void removeCertificate()} disabled={busy}>
                <Trash2 className="h-3.5 w-3.5" />
                <span>{t("admin.bimiCertRemove")}</span>
              </Button>
            )}
          </div>

          <div>
            <label htmlFor="bimi-pem" className={labelClass}>{t("admin.bimiCertPaste")}</label>
            <textarea
              id="bimi-pem"
              value={pem}
              onChange={(e) => setPem(e.target.value)}
              rows={4}
              spellCheck={false}
              placeholder="-----BEGIN CERTIFICATE-----"
              className={`${inputClass} font-mono text-[12px]`}
            />
            {pem.trim() && (
              <div className="mt-2">
                <Button variant="primary" size="sm" onClick={() => void sendPem(pem)} disabled={busy}>
                  {busy ? t("common.loading") : t("admin.bimiCertSave")}
                </Button>
              </div>
            )}
          </div>

          {certProblems.length > 0 && (
            <div className="flex flex-col gap-1 rounded-xl bg-rose-900/50 px-3.5 py-3 shadow-edge">
              <span className="text-[12.5px] text-rose-100">{t("admin.bimiCertRejected")}</span>
              {certProblems.map((p) => (
                <span key={p} className="font-mono text-[11.5px] leading-relaxed text-rose-200">{p}</span>
              ))}
            </div>
          )}

          {!hasCertificate && (
            <div>
              <label htmlFor="bimi-vmc" className={labelClass}>{t("admin.bimiVmc")}</label>
              <div className="flex flex-wrap gap-2">
                <input
                  id="bimi-vmc"
                  type="url"
                  value={vmcUrl}
                  onChange={(e) => setVmcUrl(e.target.value)}
                  placeholder="https://.../vmc.pem"
                  className={`${inputClass} min-w-[220px] flex-1`}
                />
                <Button variant="secondary" size="sm" onClick={() => void saveVmcUrl()} disabled={busy || vmcUrl === (bimi?.vmc_url ?? "")}>
                  {t("common.save")}
                </Button>
              </div>
            </div>
          )}

          <input
            ref={pemRef}
            type="file"
            accept=".pem,.crt,.cer,application/x-pem-file,application/pem-certificate-chain,text/plain"
            className="hidden"
            onChange={async (e) => {
              const file = e.target.files?.[0];
              if (file) await sendPem(await file.text());
              e.target.value = "";
            }}
          />
        </div>
      )}

      {/* ------------------------------------------------------- DNS + check */}
      {bimi?.configured && (
        <div className={subPanel}>
          <h3 className="text-[14px] font-medium text-slate-100">{t("admin.bimiRecordTitle")}</h3>
          <p className="text-[12px] leading-relaxed text-slate-400">{t("admin.bimiRecordHint")}</p>
          <div className="flex flex-col gap-1.5 rounded-lg bg-dark-panel px-3.5 py-3 shadow-edge">
            <span className="font-mono text-[11.5px] text-slate-500">default._bimi.{domain} TXT</span>
            <span className="break-all font-mono text-[12px] text-slate-200">{record}</span>
            <div className="flex flex-wrap items-center gap-2">
              <Button variant="ghost" size="sm" onClick={() => void copyRecord()}>
                <Copy className="h-3.5 w-3.5" />
                <span>{copied ? t("admin.bimiCopied") : t("admin.bimiCopy")}</span>
              </Button>
              <Button variant="secondary" size="sm" onClick={() => void publishNow()} disabled={publishing}>
                <CloudUpload className="h-3.5 w-3.5" />
                <span>{publishing ? t("admin.bimiPublishing") : t("admin.bimiPublishNow")}</span>
              </Button>
            </div>
          </div>

          {bimi?.dns_publishing && <p className="text-[11.5px] leading-relaxed text-slate-500">{t("admin.bimiAutoPublish")}</p>}
          {bimi?.certificate_withheld && (
            <p className="text-[11.5px] leading-relaxed text-amber-400">
              {t("admin.bimiWithheld", { reason: bimi.certificate_withheld })}
            </p>
          )}
          {published && (
            <p role="status" className={`text-[12px] leading-relaxed ${published.ok ? "text-slate-300" : "text-rose-300"}`}>
              {published.message}
            </p>
          )}

          <div className="mt-1 flex flex-wrap items-center gap-3">
            <h3 className="text-[14px] font-medium text-slate-100">{t("admin.bimiCheckTitle")}</h3>
            <Button variant="secondary" size="sm" onClick={() => void runCheck()} disabled={checking}>
              <Play className="h-3.5 w-3.5" />
              <span>{checking ? t("admin.bimiCheckRunning") : t("admin.bimiCheckRun")}</span>
            </Button>
          </div>
          <p className="text-[12px] leading-relaxed text-slate-400">{t("admin.bimiCheckExplainer")}</p>

          {checks && (
            <div className="flex flex-col gap-2">
              <div className={`flex items-center gap-2 text-[13px] ${checks.ready ? "text-indigo-200" : "text-amber-300"}`}>
                {checks.ready ? <CheckCircle2 className="h-4 w-4" /> : <AlertTriangle className="h-4 w-4" />}
                {checks.ready ? t("admin.bimiCheckReady") : t("admin.bimiCheckNotReady")}
              </div>
              {checks.checks.map((c) => (
                <div key={c.id} className="flex flex-col gap-1 rounded-lg bg-dark-panel px-3.5 py-2.5 shadow-edge">
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge variant={statusBadge(c.status)}>{t(`admin.bimiCheckStatus_${c.status}`)}</Badge>
                    <span className="text-[12.5px] text-slate-200">{t(`admin.bimiCheckItem_${c.id}`)}</span>
                  </div>
                  <span className="break-words text-[11.5px] leading-relaxed text-slate-400">{c.detail}</span>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </section>
  );
}

