/**
 * The width of the message list, which the reader can drag to change.
 *
 * Both panes share one row, so a width has to leave the other pane enough room
 * to be usable: a list dragged to the full window would leave no reader, and
 * one dragged to nothing would leave subjects unreadable. The limits live here
 * so they are tested rather than scattered through the drag handler.
 */

export const PANE_KEY = "lm_list_width";
export const DEFAULT_LIST_WIDTH = 420;
export const MIN_LIST_WIDTH = 300;
/** What the reader keeps whatever the list is dragged to. */
export const MIN_READER_WIDTH = 520;

export function clampListWidth(width: number, containerWidth: number): number {
  if (Number.isNaN(width)) return DEFAULT_LIST_WIDTH;
  const max = Math.max(MIN_LIST_WIDTH, containerWidth - MIN_READER_WIDTH);
  return Math.round(Math.min(max, Math.max(MIN_LIST_WIDTH, width)));
}

/** The stored width, or null when there is none or it is not a number. */
export function readStoredWidth(): number | null {
  try {
    const raw = window.localStorage.getItem(PANE_KEY);
    const value = raw === null ? NaN : Number(raw);
    return Number.isFinite(value) ? value : null;
  } catch {
    // Storage can throw (private window, blocked site data); the default holds.
    return null;
  }
}

export function storeWidth(width: number): void {
  try {
    window.localStorage.setItem(PANE_KEY, String(Math.round(width)));
  } catch {
    // Not remembered this session; the divider still works.
  }
}
