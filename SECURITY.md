# Security Policy

## Supported versions

| Version | Supported |
| --- | --- |
| latest tagged release | yes |
| older releases | no — please update |

Releases are cut from `main`; tags are `vX.Y.Z`.

## Reporting a vulnerability

Use GitHub's **private vulnerability reporting**: open the repository →
**Security** tab → *Report a vulnerability*. This reaches the maintainers
directly without a public paper trail.

Please include:

- affected version / commit hash,
- the steps or inputs that trigger the issue,
- what an attacker could gain from it.

You will get an initial response within a few days. Once a fix is ready we
publish a GitHub Security Advisory with CVE credit to the reporter.

## Scope

**In scope**

- The NeuroRift application itself: privilege boundaries (it runs
  unelevated by default; TUN mode requests elevation explicitly), the local
  mihomo REST/WebSocket surface it exposes, settings/profile file handling,
  the Windows system-proxy integration, and anything that executes the
  bundled or downloaded mihomo core.
- The release pipeline in `.github/workflows/` (artifact integrity).

**Out of scope**

- The mihomo core itself — report engine issues upstream at
  [MetaCubeX/mihomo](https://github.com/MetaCubeX/mihomo/security/advisories).
  We pin the core version in the release workflow and follow upstream fixes
  closely.
- "A proxy client can be used to reach the internet" — that is the product.
- Missing code signing on public builds (known limitation, tracked in the
  issue tracker; treat downloads from GitHub Releases + the published SHA256
  sums as the integrity chain of trust).

## Supply-chain notes

- The core download path never uses the GitHub REST API; it resolves the
  latest stable tag from the `releases/latest` redirect and verifies the
  binary runs (`mihomo -v`) before activating it atomically.
- Dependency updates (Go modules, npm, Actions) arrive via Dependabot and
  land only after CI passes.
