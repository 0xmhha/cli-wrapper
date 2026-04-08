# Security Policy

## Supported Versions

Until cli-wrapper reaches v1.0.0, security fixes are only backported to `main`.
Once v1.0.0 ships, this table will track supported release branches.

| Version | Supported          |
|---------|--------------------|
| `main`  | :white_check_mark: |

## Reporting a Vulnerability

**Please do not report security vulnerabilities through public GitHub issues,
discussions, or pull requests.**

Report a suspected vulnerability through one of the following private channels:

1. **Preferred — GitHub Security Advisories:**
   [Open a private advisory](https://github.com/0xmhha/cli-wrapper/security/advisories/new).
   This is the fastest path and keeps all communication scoped to the repository.

2. **Email:** If you cannot use GitHub Advisories, open a minimal placeholder
   issue asking for a private contact — a maintainer will reach out. Do **not**
   include vulnerability details in the placeholder.

### What to include

A good report contains:

- A description of the vulnerability and its impact
- The affected version(s) or commit hash
- **Reproduction steps** — a minimal PoC if possible
- The environment (OS, Go version, sandbox provider if relevant)
- Any suggested mitigation

### What to expect

- **Acknowledgement** within 72 hours.
- **Initial assessment** within 7 days, including a severity classification.
- **Coordinated disclosure**: we will agree on a disclosure timeline with you,
  typically 30–90 days depending on severity and fix complexity.
- **Credit** in the advisory and release notes unless you prefer to remain
  anonymous.

## Scope

In scope:

- The `cliwrap` host library (`pkg/cliwrap`, `pkg/config`, `pkg/sandbox`)
- The `cliwrap` and `cliwrap-agent` binaries
- The IPC protocol and WAL format
- Sandbox providers shipped in this repository (`noop`, `scriptdir`)

Out of scope:

- Vulnerabilities in **child processes** supervised by cli-wrapper — those
  belong to the respective upstream projects
- Denial-of-service caused by **misconfiguration** (e.g. unlimited restart
  with a crashing child)
- Issues requiring **root on the host** or physical access to the machine
- **External sandbox providers** not maintained in this repository

## Security Best Practices for Users

If you embed cli-wrapper in your application, please:

- Run `cliwrap-agent` with the **least privilege** your use case allows
- Put `runtime.dir` on a **non-world-writable** path
- Use a sandbox provider (`scriptdir`, bubblewrap, etc.) for untrusted children
- Keep Go and cli-wrapper **up to date** to receive security fixes
- Set `system_budget` limits to prevent a runaway child from starving the host
- Review child process `env` and `args` carefully — cli-wrapper passes them
  through unchanged

## Cryptographic Disclosure

cli-wrapper does not implement cryptographic primitives. The IPC channel is a
Unix-domain socket scoped to `runtime.dir`, so confidentiality relies on
filesystem permissions rather than wire-level encryption. If your threat model
requires authenticated, encrypted IPC across hosts, cli-wrapper is not the
right tool.

Thank you for helping keep cli-wrapper and its users safe.
