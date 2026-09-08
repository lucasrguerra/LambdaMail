"use client";

import React, { useState } from "react";
import { initialsFor } from "../lib/initials";

interface AvatarProps {
  address: string | null | undefined;
  /** Rendered size in pixels; the square the circle is cut from. */
  size: number;
  className?: string;
  /**
   * What to show while there is no photo. Passed in where the caller already
   * has a better answer than the address - a display name, usually - so the
   * fallback does not get worse than what it replaced.
   */
  initials?: string;
}

/**
 * Someone's photo, falling back to their initials.
 *
 * The endpoint answers 404 when there is no photo, which is most people most
 * of the time, so the initials are what is rendered first and the image only
 * replaces them once it has actually loaded. Doing it the other way round
 * shows an empty circle on every row until each request comes back.
 */
export function Avatar({ address, size, className = "", initials }: AvatarProps) {
  const [loaded, setLoaded] = useState(false);
  const [failed, setFailed] = useState(false);

  const src = address ? `/api/v1/avatar?address=${encodeURIComponent(address)}` : null;

  return (
    <span
      className={`relative inline-flex flex-none items-center justify-center overflow-hidden rounded-full uppercase ${className}`}
      style={{ width: size, height: size, fontSize: Math.max(9, Math.round(size * 0.38)) }}
      aria-hidden="true"
    >
      {!loaded && <span>{initials ?? initialsFor(address)}</span>}
      {src && !failed && (
        // eslint-disable-next-line @next/next/no-img-element
        <img
          src={src}
          alt=""
          width={size}
          height={size}
          onLoad={() => setLoaded(true)}
          onError={() => setFailed(true)}
          className={`absolute inset-0 h-full w-full object-cover ${loaded ? "" : "opacity-0"}`}
        />
      )}
    </span>
  );
}
