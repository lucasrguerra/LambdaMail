/**
 * Page and filter parameters, read from a URL and made safe for SQL.
 *
 * The admin lists used to answer with a hardcoded LIMIT and no filter at all,
 * so a console with more than a couple of hundred rows simply could not reach
 * the rest of them.
 */

export const DEFAULT_PAGE_SIZE = 25;
/** Asking for a million rows must not be a thing one request can do. */
export const MAX_PAGE_SIZE = 200;
/** Long enough for an address, short enough not to be a payload. */
export const MAX_SEARCH_LENGTH = 200;

export interface PageRequest {
  page: number;
  pageSize: number;
  offset: number;
  search: string;
}

/**
 * A positive integer, or the fallback. Anything else - NaN, a float, a
 * negative, an exponent - would reach SQL as an invalid LIMIT or OFFSET.
 */
function positiveInt(raw: string | null, fallback: number, max?: number): number {
  if (raw === null) return fallback;
  const trimmed = raw.trim();
  if (!/^\d+$/.test(trimmed)) return fallback;
  const value = Number(trimmed);
  if (!Number.isSafeInteger(value) || value < 1) return fallback;
  return max !== undefined ? Math.min(value, max) : value;
}

export function parsePageParams(url: URL): PageRequest {
  const page = positiveInt(url.searchParams.get("page"), 1);
  const pageSize = positiveInt(url.searchParams.get("page_size"), DEFAULT_PAGE_SIZE, MAX_PAGE_SIZE);
  const search = (url.searchParams.get("search") ?? "").trim().slice(0, MAX_SEARCH_LENGTH);
  return { page, pageSize, offset: (page - 1) * pageSize, search };
}

/** The envelope every paginated list answers with. */
export function paged<T>(items: T[], total: number, p: PageRequest) {
  return {
    items,
    total,
    page: p.page,
    page_size: p.pageSize,
    total_pages: Math.max(1, Math.ceil(total / p.pageSize)),
  };
}
