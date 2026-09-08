"use client";

import React, { useCallback, useEffect, useRef, useState } from "react";
import { Upload, ZoomIn, ZoomOut, Trash2 } from "lucide-react";
import { Button } from "./ui/Button";
import { useTranslations } from "../i18n/provider";
import {
  clampTransform,
  initialTransform,
  scaleRange,
  sourceRect,
  type Size,
  type Transform,
} from "../lib/avatarCrop";

/** The frame is square; the mask over it is what makes the photo a circle. */
const VIEWPORT = 288;
/** What gets stored. Larger than any place it is shown, so it stays sharp. */
const EXPORT_SIZE = 512;

interface AvatarEditorProps {
  /** Current photo, if there is one. */
  src: string | null;
  onSaved: () => void;
}

export function AvatarEditor({ src, onSaved }: AvatarEditorProps) {
  const t = useTranslations();

  const [image, setImage] = useState<HTMLImageElement | null>(null);
  const [natural, setNatural] = useState<Size>({ width: 0, height: 0 });
  const [transform, setTransform] = useState<Transform>({ scale: 1, offsetX: 0, offsetY: 0 });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const fileRef = useRef<HTMLInputElement | null>(null);
  const dragRef = useRef<{ x: number; y: number; from: Transform } | null>(null);

  /** Draws the current transform into the preview. */
  const paint = useCallback(() => {
    const canvas = canvasRef.current;
    if (!canvas || !image) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    ctx.clearRect(0, 0, VIEWPORT, VIEWPORT);
    const r = sourceRect(natural, VIEWPORT, transform);
    ctx.drawImage(image, r.x, r.y, r.width, r.height, 0, 0, VIEWPORT, VIEWPORT);
  }, [image, natural, transform]);

  useEffect(() => {
    paint();
  }, [paint]);

  const openFile = (file: File) => {
    setError(null);
    const url = URL.createObjectURL(file);
    const img = new Image();
    img.onload = () => {
      const size = { width: img.naturalWidth, height: img.naturalHeight };
      setImage(img);
      setNatural(size);
      setTransform(initialTransform(size, VIEWPORT));
      // The bitmap is decoded and held; the object URL has done its job and
      // would otherwise keep the file alive for the life of the page.
      URL.revokeObjectURL(url);
    };
    img.onerror = () => {
      URL.revokeObjectURL(url);
      setError(t("avatar.notAnImage"));
    };
    img.src = url;
  };

  const onPointerDown = (e: React.PointerEvent<HTMLCanvasElement>) => {
    if (!image) return;
    (e.target as HTMLElement).setPointerCapture(e.pointerId);
    dragRef.current = { x: e.clientX, y: e.clientY, from: transform };
  };

  const onPointerMove = (e: React.PointerEvent<HTMLCanvasElement>) => {
    const drag = dragRef.current;
    if (!drag || !image) return;
    setTransform(
      clampTransform(natural, VIEWPORT, {
        scale: drag.from.scale,
        offsetX: drag.from.offsetX + (e.clientX - drag.x),
        offsetY: drag.from.offsetY + (e.clientY - drag.y),
      }),
    );
  };

  const endDrag = () => {
    dragRef.current = null;
  };

  const zoomTo = (scale: number) => {
    if (!image) return;
    setTransform((prev) => clampTransform(natural, VIEWPORT, { ...prev, scale }));
  };

  const save = async () => {
    if (!image) return;
    setBusy(true);
    setError(null);
    try {
      // Exported from the source pixels rather than from the preview, so the
      // stored photo is as sharp as the original allows instead of being
      // limited to the size of the editor on screen.
      const out = document.createElement("canvas");
      out.width = EXPORT_SIZE;
      out.height = EXPORT_SIZE;
      const ctx = out.getContext("2d");
      if (!ctx) throw new Error();
      const r = sourceRect(natural, VIEWPORT, transform);
      ctx.drawImage(image, r.x, r.y, r.width, r.height, 0, 0, EXPORT_SIZE, EXPORT_SIZE);

      const blob = await new Promise<Blob | null>((resolve) =>
        // The frame is always fully covered, so there is no transparency to
        // preserve and JPEG is a fraction of the size.
        out.toBlob((b) => resolve(b), "image/jpeg", 0.9),
      );
      if (!blob) throw new Error();

      const res = await fetch("/api/v1/user/avatar", {
        method: "PUT",
        headers: { "Content-Type": "image/jpeg" },
        body: blob,
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        throw new Error(data.message);
      }
      setImage(null);
      onSaved();
    } catch (err) {
      setError(err instanceof Error && err.message ? err.message : t("errors.serverError"));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    setBusy(true);
    setError(null);
    try {
      const res = await fetch("/api/v1/user/avatar", { method: "DELETE" });
      if (!res.ok) throw new Error();
      onSaved();
    } catch {
      setError(t("errors.serverError"));
    } finally {
      setBusy(false);
    }
  };

  const range = scaleRange(natural, VIEWPORT);

  return (
    <div className="flex flex-col gap-4">
      {!image ? (
        <div className="flex flex-wrap items-center gap-4">
          {/* What is stored today, shown as it will appear elsewhere. */}
          <span className="flex h-[88px] w-[88px] flex-none items-center justify-center overflow-hidden rounded-full bg-dark-card shadow-edge">
            {src ? (
              // eslint-disable-next-line @next/next/no-img-element
              <img src={src} alt="" className="h-full w-full object-cover" />
            ) : (
              <span className="text-[11px] text-slate-500">{t("avatar.none")}</span>
            )}
          </span>

          <div className="flex flex-wrap items-center gap-2">
            <Button variant="secondary" size="sm" onClick={() => fileRef.current?.click()} disabled={busy}>
              <Upload className="h-3.5 w-3.5" />
              <span>{src ? t("avatar.replace") : t("avatar.choose")}</span>
            </Button>
            {src && (
              <Button variant="ghost" size="sm" onClick={() => void remove()} disabled={busy}>
                <Trash2 className="h-3.5 w-3.5" />
                <span>{t("common.delete")}</span>
              </Button>
            )}
          </div>
        </div>
      ) : (
        <div className="flex flex-col items-center gap-3">
          <div
            className="relative overflow-hidden rounded-full shadow-edge"
            style={{ width: VIEWPORT, height: VIEWPORT }}
          >
            <canvas
              ref={canvasRef}
              width={VIEWPORT}
              height={VIEWPORT}
              onPointerDown={onPointerDown}
              onPointerMove={onPointerMove}
              onPointerUp={endDrag}
              onPointerCancel={endDrag}
              className="block cursor-grab touch-none active:cursor-grabbing"
              aria-label={t("avatar.dragToPosition")}
            />
          </div>

          <div className="flex w-full max-w-[288px] items-center gap-2.5">
            <ZoomOut className="h-4 w-4 flex-none text-slate-400" />
            <input
              type="range"
              min={range.min}
              max={range.max}
              step={(range.max - range.min) / 200 || 0.01}
              value={transform.scale}
              onChange={(e) => zoomTo(Number(e.target.value))}
              aria-label={t("avatar.zoom")}
              className="h-1 flex-1 cursor-pointer appearance-none rounded-full bg-dark-card accent-indigo-500"
            />
            <ZoomIn className="h-4 w-4 flex-none text-slate-400" />
          </div>

          <p className="text-[11.5px] text-slate-500">{t("avatar.dragToPosition")}</p>

          <div className="flex flex-wrap items-center justify-center gap-2">
            <Button variant="secondary" size="sm" onClick={() => setImage(null)} disabled={busy}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" size="sm" onClick={() => void save()} disabled={busy}>
              {busy ? t("common.loading") : t("common.save")}
            </Button>
          </div>
        </div>
      )}

      {error && (
        <div className="rounded-xl bg-rose-900/60 px-3.5 py-2.5 text-[12.5px] text-rose-200 shadow-edge">
          {error}
        </div>
      )}

      <input
        ref={fileRef}
        type="file"
        accept="image/png,image/jpeg,image/webp"
        className="hidden"
        onChange={(e) => {
          const file = e.target.files?.[0];
          if (file) openFile(file);
          // Cleared so choosing the same file twice in a row still fires.
          e.target.value = "";
        }}
      />
    </div>
  );
}
