import { defineConfig } from "@playwright/test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dirname, "..");
const config = Object.fromEntries(
  readFileSync(resolve(root, ".env"), "utf8")
    .split("\n")
    .filter((line) => line && !line.startsWith("#"))
    .map((line) => {
      const at = line.indexOf("=");
      return [line.slice(0, at), line.slice(at + 1)];
    }),
);
const database = new URL(process.env.TEST_DATABASE_URL || config.DATABASE_URL!);
if (!database.pathname.endsWith("_test")) database.pathname += "_test";

export default defineConfig({
  testDir: "./e2e",
  workers: 1,
  retries: 0,
  timeout: 60000,
  use: {
    baseURL: "http://127.0.0.1:8089",
    headless: true,
    channel: process.env.PLAYWRIGHT_CHANNEL || undefined,
    viewport: { width: 1440, height: 1060 },
    screenshot: "only-on-failure",
  },
  webServer: [
    {
      command: "node e2e/upstream.mjs",
      url: "http://127.0.0.1:8099",
      reuseExistingServer: false,
    },
    {
      command: "go run ./cmd/server",
      cwd: root,
      url: "http://127.0.0.1:8089/api/health",
      reuseExistingServer: false,
      env: {
        APP_ADDR: "127.0.0.1:8089",
        DATABASE_URL: database.toString(),
        UPSTREAM_ORIGINS: "http://127.0.0.1:8099",
      },
      timeout: 60000,
    },
  ],
});
