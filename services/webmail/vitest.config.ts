import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    // jsdom, not node: the sanitizer needs a DOM to parse with, and under the
    // default node environment DOMPurify never ran at all - the suite passed
    // while testing only the fallback path.
    environment: "jsdom",
  },
});
