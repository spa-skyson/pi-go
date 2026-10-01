import { describe, expect, it } from "vitest";
import { explainPirateError, renderPirateErrorMarkdown } from "../src/errorInfo";

describe("Pi-rate error explanations", () => {
  it("explains provider quota failures with recovery steps", () => {
    const info = explainPirateError(
      new Error("429 Too Many Requests: monthly usage limit reached"),
      { command: "pirate", args: ["acp-server"], cwd: "/tmp/project" },
    );
    expect(info.title).toContain("provider limit");
    expect(info.detail).toContain("429 Too Many Requests");
    expect(info.steps.join(" ")).toContain("quota");
    expect(renderPirateErrorMarkdown(info)).toContain("pirate.command");
  });

  it("explains a missing command as a VS Code setting problem", () => {
    const info = explainPirateError(
      Object.assign(new Error("spawn pirate ENOENT"), { code: "ENOENT" }),
      { command: "pirate", args: ["acp-server"], cwd: "/tmp/project" },
    );
    expect(info.title).toContain("could not start");
    expect(info.steps.join(" ")).toContain("absolute path");
    expect(info.detail).toContain("/tmp/project");
  });

  it("redacts sensitive launch arguments", () => {
    const info = explainPirateError(new Error("provider failed"), {
      command: "pirate",
      args: ["acp-server", "--header", "Authorization=Bearer secret-value"],
      cwd: "/tmp/project",
    });
    expect(info.detail).not.toContain("secret-value");
    expect(info.detail).toContain("[redacted]");
  });

  it("does not treat a missing model as a missing executable", () => {
    const info = explainPirateError(new Error("model not found"), {
      command: "pirate",
      args: ["acp-server"],
      cwd: "/tmp/project",
    });
    expect(info.title).toBe("Pi-rate could not complete the request");
  });

  it("explains ACP internal errors as a session/provider problem", () => {
    const info = explainPirateError(new Error("Internal error"), {
      command: "pirate",
      args: ["acp-server"],
      cwd: "/tmp/project",
    });
    expect(info.title).toContain("internal error");
    expect(info.steps.join(" ")).toContain("new session");
  });
});
