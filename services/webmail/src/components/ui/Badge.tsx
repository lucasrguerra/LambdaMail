import React from 'react';
import { clsx } from 'clsx';
import { twMerge } from 'tailwind-merge';

interface BadgeProps {
  variant?: 'success' | 'warning' | 'danger' | 'info' | 'neutral';
  children: React.ReactNode;
  className?: string;
}

/**
 * A tag: a small tinted label from the ramps.
 *
 * Nocturne tags are a deep step of a ramp filled behind a light step of the
 * same ramp - no border, no blur, and no rounded-full pill, which at this size
 * read as a status light rather than a label. `success` and `info` are the same
 * accent because this palette is mono: what separates them is the word inside.
 * Nothing here is sized by its text, so a translated label wraps instead of
 * being clipped.
 */
export function Badge({ variant = 'neutral', children, className }: BadgeProps) {
  const baseStyles =
    'inline-flex items-center gap-1.5 rounded-md px-2.5 py-0.5 text-[11px] font-medium leading-relaxed tracking-[0.02em]';

  const variants = {
    success: 'bg-indigo-800 text-indigo-100',
    info: 'bg-indigo-900 text-indigo-200',
    warning: 'bg-amber-900 text-amber-200',
    danger: 'bg-rose-900 text-rose-200',
    neutral: 'bg-slate-800 text-slate-300',
  };

  return (
    <span className={twMerge(clsx(baseStyles, variants[variant], className))}>
      {children}
    </span>
  );
}
