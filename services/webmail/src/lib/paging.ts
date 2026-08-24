/**
 * The page numbers a pager should offer.
 *
 * Rendering every page is fine for three and unusable for four hundred, so the
 * window follows the current page and the ends stay reachable.
 */
export function pageWindow(page: number, totalPages: number, span = 2): number[] {
  if (!Number.isFinite(totalPages) || totalPages < 1) return [1];
  const current = Math.min(Math.max(Math.trunc(page) || 1, 1), totalPages);

  const first = Math.max(1, current - span);
  const last = Math.min(totalPages, current + span);

  const pages: number[] = [];
  for (let p = first; p <= last; p++) pages.push(p);
  return pages;
}

/** The human sentence under a list: which rows these are, out of how many. */
export function pageRange(page: number, pageSize: number, total: number): { from: number; to: number } {
  if (total <= 0) return { from: 0, to: 0 };
  const from = (page - 1) * pageSize + 1;
  return { from, to: Math.min(total, page * pageSize) };
}
