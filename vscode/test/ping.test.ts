import { describe, expect, it } from "vitest";
import { formatPiratePingResult, runPiratePing, sanitizePiratePingOutput } from "../src/ping";

describe("Pi-rate ping output", () => {
  it("removes credential and authorization values from provider output", () => {
    const output = sanitizePiratePingOutput(
      "API Key: agw_secret_value\n> Authorization: Bearer abc123\n* Model replied: ok",
    );
    expect(output).not.toContain("agw_secret_value");
    expect(output).not.toContain("abc123");
    expect(output).toContain("API Key: [redacted]");
    expect(output).toContain("Authorization: [redacted]");
  });

  it("redacts credentials embedded in request URLs", () => {
    const output = sanitizePiratePingOutput("HTTP FAILED https://example.test/v1/models?key=secret-value&region=us");
    expect(output).not.toContain("secret-value");
    expect(output).toContain("key=[redacted]");
    expect(output).toContain("region=us");
  });

  it("formats a successful ping as a compact chat result", () => {
    const result = formatPiratePingResult({
      exitCode: 0,
      stdout: "Provider: agentgateway\nModel: ollama-deepseek\n✓ Prompt OK — model is ALIVE",
      stderr: "",
    });
    expect(result.ok).toBe(true);
    expect(result.title).toBe("Pi-rate ping succeeded");
    expect(result.detail).toContain("agentgateway");
    expect(result.detail).toContain("ollama-deepseek");
  });

  it("treats a skipped model probe as a failed ping even when the CLI exits zero", () => {
    const result = formatPiratePingResult({
      exitCode: 0,
      stdout: "* RESULT: connection issue — HTTP request failed\n* Skipped — endpoint not reachable",
      stderr: "",
    });
    expect(result.ok).toBe(false);
    expect(result.title).toBe("Pi-rate ping failed");
  });

  it("runs the configured command with the ping subcommand", async () => {
    const result = await runPiratePing({ command: "/bin/echo", args: ["acp-server", "--model", "ollama-deepseek"], cwd: "/tmp" });
    expect(result.ok).toBe(true);
    expect(result.detail).toBe("ping --model ollama-deepseek");
  });
});
