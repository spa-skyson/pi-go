import { spawn } from "node:child_process";

export interface PiratePingConfig {
  command: string;
  args?: string[];
  cwd: string;
}

export interface PiratePingProcessResult {
  exitCode: number | null;
  stdout: string;
  stderr: string;
}

export interface PiratePingResult {
  ok: boolean;
  title: string;
  detail: string;
}

/** Remove ANSI styling and values that should never be copied into chat. */
export function sanitizePiratePingOutput(output: string): string {
  return output
    .replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, "")
    .replace(/(API\s*Key\s*:\s*).*/gi, "$1[redacted]")
    .replace(/(Authorization\s*:\s*).*/gi, "$1[redacted]")
    .replace(/(Bearer\s+)[A-Za-z0-9._~+/=-]+/gi, "$1[redacted]")
    .replace(/([?&](?:api[-_]?key|key|token|access_token|authorization|secret|password)=)[^&\s]+/gi, "$1[redacted]")
    .trim();
}

export function formatPiratePingResult(result: PiratePingProcessResult): PiratePingResult {
  const output = compactPingOutput(sanitizePiratePingOutput([result.stdout, result.stderr].filter(Boolean).join("\n")));
  const unhealthy = /RESULT:\s*(?:connection issue|failure)|skipped\s*[—-]\s*endpoint not reachable|HTTP FAILED|authentication failed|server error|rate limited|model ping failed/i.test(output);
  if (result.exitCode === 0 && !unhealthy) {
    return {
      ok: true,
      title: "Pi-rate ping succeeded",
      detail: output || "The configured provider responded successfully.",
    };
  }
  return {
    ok: false,
    title: "Pi-rate ping failed",
    detail: output || `pirate ping exited with code ${String(result.exitCode ?? "unknown")}.`,
  };
}

function compactPingOutput(output: string): string {
  const lines = output.split("\n").filter((line) => line.trim().length > 0);
  const useful = lines.filter((line) =>
    /provider:|model:|ollama:|base url:|status:|connected|prompt|model replied|alive|failed|error|endpoint/i.test(line),
  );
  const selected = (useful.length ? useful : lines).slice(0, 12);
  const text = selected.join("\n");
  return text.length > 1600 ? `${text.slice(0, 1597)}…` : text;
}

export function runPiratePing(config: PiratePingConfig, timeoutMs = 15_000): Promise<PiratePingResult> {
  return new Promise((resolve) => {
    let stdout = "";
    let stderr = "";
    let settled = false;
    const pingArgs = [
      "ping",
      ...(config.args ?? []).filter((arg) => arg !== "acp-server" && arg !== "--acp-server"),
    ];
    const child = spawn(config.command, pingArgs, { cwd: config.cwd, stdio: ["ignore", "pipe", "pipe"] });
    const finish = (result: PiratePingResult): void => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      resolve(result);
    };
    const timer = setTimeout(() => {
      child.kill();
      finish({ ok: false, title: "Pi-rate ping timed out", detail: `pirate ping did not finish within ${timeoutMs} ms.` });
    }, timeoutMs);
    child.stdout?.on("data", (chunk: Buffer | string) => { stdout += chunk.toString(); });
    child.stderr?.on("data", (chunk: Buffer | string) => { stderr += chunk.toString(); });
    child.once("error", (error) => {
      finish({ ok: false, title: "Pi-rate ping could not start", detail: sanitizePiratePingOutput(error.message) });
    });
    child.once("close", (exitCode) => finish(formatPiratePingResult({ exitCode, stdout, stderr })));
  });
}
