/**
 * Nocturne, expressed as Tailwind scales.
 *
 * The redesign replaces two visual languages with one: the webmail's indigo and
 * the console's green both become the single blurple accent, and every grey
 * comes from one perceptual ramp instead of Tailwind's stock slate. Rather than
 * rename several thousand utility classes across the screens, the scales those
 * classes already name are remapped onto the design system's own values - so
 * `bg-slate-900`, `text-emerald-400` and `border-indigo-500/30` all resolve
 * into Nocturne, and a colour cannot enter the interface from outside it.
 *
 * The values here are the same ones globals.css carries as CSS variables.
 *
 * @type {import('tailwindcss').Config}
 */

// The neutral ramp, ordered the way Tailwind expects (light to dark) so the
// existing `text-slate-400` / `bg-slate-900` idiom keeps meaning what it meant.
const neutral = {
  50: '#f3f5fe',
  100: '#e9e9ed', // body text
  200: '#e4e7f5',
  300: '#cfd3e5',
  400: '#9397ab', // muted text
  500: '#75798c',
  600: '#595d6c',
  700: '#3f424d', // borders
  800: '#232532', // card
  900: '#1b1c28', // panel
  950: '#161826', // page ground
};

// The one accent, in the product's own blurple (OKLCH hue 289).
const accent = {
  50: '#faf9ff',
  100: '#f5f4ff',
  200: '#e7e5fe',
  300: '#d2cefd',
  400: '#b5abfc',
  500: '#9184d9',
  600: '#796cbf',
  700: '#5d5294',
  800: '#423a6a',
  900: '#2b2741',
  950: '#211d33',
};

// Warning and danger are the only other hues, and both are desaturated: on this
// ground a saturated amber or rose reads as an error in the design, not in the
// data.
const warn = {
  50: '#fbf8ee',
  100: '#f6efd8',
  200: '#ecdfb2',
  300: '#dfcb8b',
  400: '#cbb46a',
  500: '#b39b52',
  600: '#8f7b41',
  700: '#6b5c31',
  800: '#473d21',
  900: '#2f2917',
  950: '#1f1b10',
};

const bad = {
  50: '#fcf3f4',
  100: '#f8e3e5',
  200: '#f0c8cc',
  300: '#e2a8ae',
  400: '#d08a92',
  500: '#b96f78',
  600: '#985860',
  700: '#74434a',
  800: '#4e2e33',
  900: '#3a2a2d',
  950: '#241a1c',
};

module.exports = {
  darkMode: 'class',
  content: [
    './src/pages/**/*.{js,ts,jsx,tsx,mdx}',
    './src/components/**/*.{js,ts,jsx,tsx,mdx}',
    './src/app/**/*.{js,ts,jsx,tsx,mdx}',
  ],
  theme: {
    extend: {
      colors: {
        slate: neutral,
        gray: neutral,
        zinc: neutral,
        neutral: neutral,
        // Every accent name in the codebase collapses onto the one accent. The
        // console's emerald and teal are the point of the exercise: the two
        // surfaces are one product and now look like it.
        indigo: accent,
        violet: accent,
        purple: accent,
        emerald: accent,
        teal: accent,
        cyan: accent,
        sky: accent,
        blue: accent,
        brand: accent,
        amber: warn,
        yellow: warn,
        orange: warn,
        rose: bad,
        red: bad,
        pink: bad,
        dark: {
          rail: '#12131f',
          bg: '#161826',
          panel: '#1b1c28',
          card: '#232532',
          border: 'rgba(233, 233, 237, 0.09)',
        },
      },
      fontFamily: {
        sans: ['Inter', 'system-ui', '-apple-system', 'sans-serif'],
        mono: ['ui-monospace', 'Menlo', 'Consolas', 'monospace'],
      },
      borderRadius: {
        xl: '12px',
        '2xl': '14px',
      },
      boxShadow: {
        // On a dark ground elevation is an edge plus ambient darkness, never a
        // stack of coloured glows.
        edge: 'inset 0 0 0 1px rgba(233, 233, 237, 0.09)',
        'edge-accent': 'inset 0 0 0 1px rgba(145, 132, 217, 0.4)',
        lift: '0 0 0 1px #3f424d, 0 16px 40px rgba(0, 0, 0, 0.5)',
      },
    },
  },
  plugins: [],
};
