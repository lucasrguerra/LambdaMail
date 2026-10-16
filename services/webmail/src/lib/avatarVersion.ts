/**
 * Which version of each person's photo this browser should ask for.
 *
 * The photo endpoint is cached by the browser, and the URL is the cache key.
 * Only the settings page used to add a version to it, so after a new photo was
 * saved the header, the message list and the reader kept showing the old one
 * from cache until it expired. Every <Avatar> now reads its version from here,
 * so one bump refetches the photo everywhere on screen, and in the other open
 * tabs of the same browser.
 */

type Listener = () => void;

const CHANNEL = "lambdamail-avatar";

const versions = new Map<string, number>();
const listeners = new Set<Listener>();
let channel: BroadcastChannel | null = null;

function key(address: string | null | undefined): string {
  return (address ?? "").trim().toLowerCase();
}

function openChannel(): BroadcastChannel | null {
  if (channel) return channel;
  if (typeof BroadcastChannel === "undefined") return null;
  channel = new BroadcastChannel(CHANNEL);
  channel.onmessage = (event: MessageEvent<{ address?: string; version?: number }>) => {
    const { address, version } = event.data ?? {};
    if (typeof address === "string" && typeof version === "number") {
      apply(address, version);
    }
  };
  return channel;
}

function apply(address: string, version: number): void {
  const k = key(address);
  if (!k || (versions.get(k) ?? 0) >= version) return;
  versions.set(k, version);
  listeners.forEach((listener) => listener());
}

/** 0 until the photo is known to have changed in this session. */
export function avatarVersionOf(address: string | null | undefined): number {
  return versions.get(key(address)) ?? 0;
}

/** Call after saving or removing a photo: everything showing it refetches. */
export function bumpAvatar(address: string | null | undefined, now: number = Date.now()): void {
  const k = key(address);
  if (!k) return;
  // Strictly increasing, so two bumps in the same millisecond still differ
  const version = Math.max(now, (versions.get(k) ?? 0) + 1);
  apply(k, version);
  openChannel()?.postMessage({ address: k, version });
}

export function subscribeAvatarVersions(listener: Listener): () => void {
  openChannel();
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}
