// Load the admin's own dotenv sources before Next selects its listening port.
import { spawn } from "node:child_process";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const nextRequire = createRequire(require.resolve("next/package.json"));
const { loadEnvConfig } = nextRequire("@next/env");
const [command = "dev", ...args] = process.argv.slice(2);
process.env.NODE_ENV ??= command === "dev" ? "development" : "production";
loadEnvConfig(process.cwd(), command === "dev");
const child = spawn(
  process.execPath,
  [require.resolve("next/dist/bin/next"), command, ...args],
  {
    stdio: "inherit",
    env: process.env,
  },
);
for (const signal of ["SIGINT", "SIGTERM"]) {
  process.on(signal, () => child.kill(signal));
}
child.on("error", (error) => {
  console.error(error.message);
  process.exitCode = 1;
});
child.on("exit", (code) => {
  process.exitCode = code ?? 1;
});
