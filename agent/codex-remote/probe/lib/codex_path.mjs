import { existsSync } from "node:fs";

// ChatGPT 26.924.20706 (codex 0.158.0-alpha.2) replaced the flat
// Resources/codex binary with the install-context layoutVersion 1 package
// (codex-package.json + bin/codex wrapper + CodexCLI.app/Contents/MacOS/codex).
// Older releases keep the flat binary, so it stays as the fallback candidate.
const candidates = [
  "/Applications/ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex",
  "/Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex",
  "/Applications/ChatGPT.app/Contents/Resources/codex",
];

export function resolveCodexPath() {
  for (const p of candidates) {
    if (existsSync(p)) return p;
  }
  return candidates[candidates.length - 1];
}
