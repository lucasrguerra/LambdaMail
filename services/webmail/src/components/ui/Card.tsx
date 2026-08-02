import React from 'react';
import { clsx } from 'clsx';
import { twMerge } from 'tailwind-merge';

interface CardProps extends React.HTMLAttributes<HTMLDivElement> {
  children: React.ReactNode;
  hoverable?: boolean;
}

export function Card({ children, className, hoverable = false, ...props }: CardProps) {
  return (
    <div
      className={twMerge(
        clsx(
          'glass-card rounded-xl p-5 border border-slate-800/80 shadow-lg',
          hoverable && 'glass-card-hover cursor-pointer',
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
  return <div className={twMerge(clsx('flex items-center justify-between mb-4', className))}>{children}</div>;
}

export function CardTitle({ children, className }: { children: React.ReactNode; className?: string }) {
  return <h3 className={twMerge(clsx('text-lg font-semibold text-slate-100 tracking-tight', className))}>{children}</h3>;
}
