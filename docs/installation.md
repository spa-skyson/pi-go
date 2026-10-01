# Installation

How to get Pi-rate onto your machine, and how to check that the binary you got
is the one that was built here.

- [Quick install](#quick-install) — one command, macOS/Linux/Windows
- [NixOS / Nix](#nixos--nix) — flake and NixOS module
- [go install](#go-install)
- [Build from source](#build-from-source)
- [Pre-built binaries](#pre-built-binaries)
- [Verifying a release](#verifying-a-release) — attestations, SBOM, `pirate verify`
- [Requirements](#requirements)

## Quick install

**macOS / Linux**

```bash
curl -fsSL https://raw.githubusercontent.com/spa-skyson/pi-rate/main/scripts/install.sh | bash
```

This script detects your OS/arch, downloads the latest release binary, and installs it to `/usr/local/bin` (or `~/.local/bin` if needed).

**Windows**

```powershell
powershell -NoProfile -Command "iwr https://raw.githubusercontent.com/spa-skyson/pi-rate/main/scripts/install.ps1 -UseBasicParsing | iex"
```

Or, from a checkout:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/install.ps1
```

Installs `pirate.exe` to `%LOCALAPPDATA%\Programs` and adds that directory to your
user `PATH`. Restart the terminal afterwards so the new `PATH` is picked up.
Runs on Windows PowerShell 5.1 (built into Windows 10/11) and PowerShell 7+;
`windows/amd64` only. Set `GITHUB_TOKEN` if you hit the GitHub API rate limit
while it resolves the latest release.

The installer checks the download against the release's `checksums.txt` and
refuses to install on a mismatch. That catches a corrupted or swapped archive,
but not a substituted release — `checksums.txt` travels the same path as the
archive. Run `pirate verify` afterwards for the provenance check that does answer
that question — see [Verifying a release](#verifying-a-release).

### Windows note: shell syntax in prompts

Windows machines without `bash.exe` on `PATH` — a stock Windows install has
none — run agent commands through `powershell.exe` instead, so write PowerShell
syntax in prompts: `;` or a newline rather than `&&`. Installing
[Git for Windows](https://git-scm.com/download/win) puts a `bash` on `PATH` and
restores the bash behaviour.

## NixOS / Nix

The repository includes a flake that builds Pi-rate reproducibly and exposes a
NixOS module. To install it in a NixOS configuration, add the repository as an
input:

```nix
# flake.nix
{
  inputs.pi-go.url = "github:spa-skyson/pi-rate";

  outputs = { self, nixpkgs, pi-go, ... }: {
    nixosConfigurations.my-host = nixpkgs.lib.nixosSystem {
      system = "x86_64-linux";
      modules = [
        ./configuration.nix
        pi-go.nixosModules.default
      ];
    };
  };
}
```

Enable it in `configuration.nix`:

```nix
{ programs.pi-go.enable = true; }
```

Then rebuild with `sudo nixos-rebuild switch --flake .`. For a one-off use,
run `nix run github:spa-skyson/pi-rate` or install it into a profile with
`nix profile install github:spa-skyson/pi-rate`.

## go install

```bash
go install github.com/spa-skyson/pi-rate/cmd/pirate@latest
```

Make sure your `GOPATH/bin` is in your `PATH`. The binary is installed as `pirate`.

## Build from source

```bash
git clone https://github.com/spa-skyson/pi-rate.git
cd pi-rate
make build
make install
```

Or without make:

```bash
git clone https://github.com/spa-skyson/pi-rate.git
cd pi-rate
go install ./cmd/pirate
```

### Development build targets

```bash
make build      # build the pirate binary
make test       # run unit tests
make lint       # golangci-lint (vet, staticcheck, errcheck, …)
make e2e        # run E2E integration tests
make clean      # remove binary
```

## Pre-built binaries

Download the latest release for your platform from the [Releases page](https://github.com/spa-skyson/pi-rate/releases).

## Verifying a release

`pirate verify` checks the running binary against the attestations published for
it, with no other tooling required:

```bash
pirate verify                                     # the running binary
pirate verify ./pirate                           # a specific file
pirate verify pi-rate_1.2.3_linux_amd64.tar.gz   # a downloaded archive, before extracting
pirate verify --json                             # machine-readable
pirate verify --sbom > sbom.spdx.json            # print the attested SBOM document
```

Example output:

```
/usr/local/bin/pirate
  sha256:abd70659b49183320320426af4abf34555b031e432aff27afbdbf1be39e1ecff

  ✓ build provenance
      repository  github.com/spa-skyson/pi-rate
      workflow    .github/workflows/release.yml@refs/tags/v1.2.3
      commit      4086645aa1f2c3d4e5f60718293a4b5c6d7e8f90
      run         https://github.com/spa-skyson/pi-rate/actions/runs/1234/attempts/1
      signer      https://github.com/spa-skyson/pi-rate/.github/workflows/release.yml@refs/tags/v1.2.3
      signed      2026-08-21T12:00:00Z

  ✓ SBOM
      format      SPDX 2.3
      packages    192
      ecosystems  golang 180, github 12
      signer      https://github.com/spa-skyson/pi-rate/.github/workflows/release.yml@refs/tags/v1.2.3
      signed      2026-08-21T12:00:00Z
```

The check starts from the file's SHA-256 and nothing else — the version
compiled into the binary, the name it was installed under and the URL it came
from are all attacker-controlled. That digest is looked up in GitHub's
attestations API and the returned [Sigstore](https://www.sigstore.dev/) bundles
are verified against the public-good Sigstore trust root: certificate chain,
Rekor transparency-log inclusion, signed certificate timestamp, and a
certificate identity that must name **this repository's release workflow,
running on a tag**. A signature from any other workflow, branch or repository
is rejected.

A binary you built yourself has no attestation and reports as unverified. That
is the expected answer, not a failure.

Verification needs network access. The Sigstore trust root is cached in
`~/.pirate/sigstore` after the first run.

### Verifying with the GitHub CLI

The same attestations are readable by `gh`, if you would rather not trust the
binary to vouch for itself:

```bash
gh attestation verify ./pirate --repo spa-skyson/pi-rate

# SBOM attestation. The predicate type carries the SPDX version syft emitted.
gh attestation verify ./pirate --repo spa-skyson/pi-rate \
  --predicate-type https://spdx.dev/Document/v2.3
```

### What is attested, and what is published

Both the release archives **and the raw binaries inside them** are attestation
subjects. `scripts/install.sh` extracts the binary and puts it on your PATH, so
the archive digest is not the digest you end up running; attesting only the
archive would leave the installed binary unverifiable. The binaries themselves
are not published as release assets — the digest is all verification needs.

Each release publishes SBOMs as assets, in SPDX JSON:

- `pi-rate_<version>_<os>_<arch>.tar.gz.sbom.json` — cataloged by syft from the
  contents of that specific archive.
- `pi-rate_<version>_sbom.spdx.json` — the aggregate SBOM cataloged from the
  source tree, and the one the SBOM attestation binds to. It covers the Go
  module graph, which is the same across every platform in the build matrix,
  plus the pinned GitHub Actions the release itself was built with.

Regenerate the aggregate SBOM locally with `make sbom` (requires
[syft](https://github.com/anchore/syft)).

## Requirements

- Go 1.27+ (only when building from source)
- At least one LLM provider API key or a running Ollama instance — see
  [Usage → API keys](usage.md#api-keys)

## Next steps

- [Usage](usage.md) — run your first session
- [Configuration](configuration.md) — roles, providers, permissions, themes
