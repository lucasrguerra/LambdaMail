/**
 * A byte count as a person reads it.
 *
 * The admin list rounded bytes to whole megabytes, so a mailbox holding
 * 137.5 KB displayed as "0 / 2 MB" and its usage bar sat empty - the figure
 * was right in the database and lost on the way to the screen.
 */
export function formatBytes(bytes: number): string {
  const value = Number(bytes);
  if (!Number.isFinite(value) || value <= 0) return "0 B";

  const units = ["B", "KB", "MB", "GB", "TB"];
  let n = value;
  let unit = 0;
  while (n >= 1024 && unit < units.length - 1) {
    n /= 1024;
    unit++;
  }

  // Bytes are whole things; above that always one decimal, which is what
  // tells 137.5 KB from 137 KB. Dropping it on larger numbers would have hid
  // exactly the digit that made the reading recognisable.
  const rendered = unit === 0 ? String(Math.round(n)) : n.toFixed(1);
  return `${rendered} ${units[unit]}`;
}

/**
 * How full a mailbox is, as a percentage, computed from the raw byte counts.
 *
 * A quota of zero means unlimited, so it is never full - dividing by it would
 * report every unlimited mailbox as over its limit.
 */
export function usageRatio(usedBytes: number, quotaBytes: number): number {
  const used = Number(usedBytes);
  const quota = Number(quotaBytes);
  if (!Number.isFinite(used) || !Number.isFinite(quota) || quota <= 0 || used <= 0) return 0;
  return Math.min(used / quota, 1);
}
