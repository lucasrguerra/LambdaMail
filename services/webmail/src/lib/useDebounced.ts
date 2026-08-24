"use client";

import { useEffect, useState } from "react";

/**
 * Holds a value still until typing stops.
 *
 * Every keystroke in a search box would otherwise be one more query against
 * the database, and the answers can arrive out of order - so the list flickers
 * between results for "an", "ana" and "a".
 */
export function useDebounced<T>(value: T, delayMs = 300): T {
  const [settled, setSettled] = useState(value);

  useEffect(() => {
    const timer = setTimeout(() => setSettled(value), delayMs);
    return () => clearTimeout(timer);
  }, [value, delayMs]);

  return settled;
}
