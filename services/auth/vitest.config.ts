import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    // session.ts refuses to load without a real signing secret, and ESM hoists
    // imports - so the value has to exist before any test file is evaluated,
    // which an assignment inside a test file cannot guarantee.
    env: {
      JWT_SECRET: process.env.JWT_SECRET ?? "a-test-signing-secret-long-enough-for-the-guard",
      LAMBDAMAIL_MASTER_KEY: process.env.LAMBDAMAIL_MASTER_KEY ?? "test-master-key-at-least-16-chars",
    },
  },
});
