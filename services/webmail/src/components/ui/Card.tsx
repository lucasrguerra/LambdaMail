import React from 'react';
import { clsx } from 'clsx';
import { twMerge } from 'tailwind-merge';

interface CardProps extends React.HTMLAttributes<HTMLDivElement> {
  children: React.ReactNode;
  hoverable?: boolean;
}

/**
 * A panel: a solid ground with a hairline inset ring.
 *
 * The edge is drawn with a ring rather than a border so a card that becomes
 * active does not move its content by a pixel, and so nesting a tile inside a
 * panel does not accumulate borders. No blur, and no drop shadow - on a dark
 * ground elevation is the edge itself.
 */
export function Card({ children, className, hoverable = false, ...props }: CardProps) {
  return (
    <div
      className={twMerge(
        clsx(
          'rounded-2xl bg-dark-panel p-5 shadow-edge',
          hoverable && 'cursor-pointer transition-colors hover:bg-dark-card hover:shadow-edge-accent',
          className
        )
      )}
      {...props}
    >
      {children}
    </div>
  );
}

export function CardHeader({ children, className }: { children: React.ReactNode; className?: string }) {
  return (
    <div className={twMerge(clsx('mb-4 flex flex-wrap items-center justify-between gap-3', className))}>
      {children}
    </div>
  );
}

export function CardTitle({ children, className }: { children: React.ReactNode; className?: string }) {
  return (
    <h3 className={twMerge(clsx('text-[17px] font-medium leading-tight text-slate-100', className))}>
      {children}
    </h3>
  );
}
