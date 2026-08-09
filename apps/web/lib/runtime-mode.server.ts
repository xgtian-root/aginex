import { probeRuntimeMode, resolveServerAPIURL } from "./runtime-mode";

export function getRuntimeMode() {
  return probeRuntimeMode(resolveServerAPIURL(process.env));
}
