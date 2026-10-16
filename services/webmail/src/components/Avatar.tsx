"use client";

import React, { useEffect, useState, useSyncExternalStore } from "react";
import { initialsFor } from "../lib/initials";
import { avatarVersionOf, subscribeAvatarVersions } from "../lib/avatarVersion";

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

  // Bumped when this person's photo is saved or removed, here or in another
  // tab, so the browser asks again instead of reusing what it cached
  const version = useSyncExternalStore(
    subscribeAvatarVersions,
    () => avatarVersionOf(address),
    () => 0,
  );

  const src = address
    ? `/api/v1/avatar?address=${encodeURIComponent(address)}${version ? `&v=${version}` : ""}`
    : null;

  // A new version may turn "no photo" (404) into a photo, or the reverse
  useEffect(() => {
    setLoaded(false);
    setFailed(false);
  }, [version]);

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
