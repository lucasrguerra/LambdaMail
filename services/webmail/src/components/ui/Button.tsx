import React from 'react';
import { clsx } from 'clsx';
import { twMerge } from 'tailwind-merge';

interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: 'primary' | 'secondary' | 'outline' | 'danger' | 'ghost';
  size?: 'sm' | 'md' | 'lg';
  children: React.ReactNode;
}

/**
 * Nocturne buttons are outlined, not filled.
 *
 * The primary action is a 1px accent border on transparent with a tinted hover
 * and a deeper tint when pressed. A flooded accent was the loudest thing on
 * every screen and made three buttons in a row compete with the content; an
 * outline states the same intent at the weight it deserves. Focus is the
 * system's own 2px accent ring from globals.css rather than a per-variant
 * offset ring.
 */
export function Button({
  variant = 'primary',
  size = 'md',
  className,
  children,
  ...props
}: ButtonProps) {
  const baseStyles =
    'inline-flex items-center justify-center whitespace-nowrap font-medium rounded-[10px] border transition-colors duration-150 disabled:opacity-45 disabled:cursor-not-allowed';

  const variants = {
    primary:
      'border-indigo-500 text-indigo-500 bg-transparent hover:bg-indigo-500/[0.12] active:bg-indigo-500/[0.22]',
    secondary:
      'border-white/[0.14] text-slate-100 bg-transparent hover:bg-white/[0.07] active:bg-white/[0.14]',
    outline:
      'border-white/[0.14] text-slate-300 bg-transparent hover:text-slate-100 hover:bg-white/[0.07] active:bg-white/[0.14]',
    danger:
      'border-rose-400 text-rose-400 bg-transparent hover:bg-rose-400/[0.12] active:bg-rose-400/[0.22]',
    ghost:
      'border-transparent text-indigo-500 bg-transparent hover:bg-indigo-500/10 active:bg-indigo-500/[0.18]',
  };

  const sizes = {
    sm: 'px-2.5 py-1.5 text-xs gap-1.5',
    md: 'px-3 py-2 text-sm gap-2',
    lg: 'px-4 py-2.5 text-[15px] gap-2.5',
  };

  return (
    <button
      className={twMerge(clsx(baseStyles, variants[variant], sizes[size], className))}
      {...props}
    >
      {children}
    </button>
  );
}
