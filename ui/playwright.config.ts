import { defineConfig } from "@playwright/test";

const dataDir = `/tmp/llm-proxy-e2e-${Date.now()}`;

export default defineConfig({
  testDir: "./e2e",
  timeout: 60_000,
  fullyParallel: false,
  workers: 1,
  use: {
    baseURL: "http://127.0.0.1:18080",
    trace: "retain-on-failure",
  },
  webServer: [
    {
      command: "FAKE_UPSTREAM_LISTEN=127.0.0.1:18081 go run ./cmd/fake-upstream",
      cwd: "..",
      port: 18081,
      reuseExistingServer: true,
      timeout: 120_000,
    },
    {
      command: `LLMPROXY_LISTEN=127.0.0.1:18080 LLMPROXY_DATA_DIR=${dataDir} go run ./cmd/llm-proxy`,
      cwd: "..",
      port: 18080,
      reuseExistingServer: true,
      timeout: 120_000,
    },
  ],
});
