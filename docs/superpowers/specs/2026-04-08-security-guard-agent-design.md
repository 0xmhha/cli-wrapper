# Security Guard Agent — Design Specification

**Date:** 2026-04-08
**Status:** Draft — pending user review
**Owner:** mhha
**Project:** `codeguard` *(working name, separate repository — not part of cli-wrapper)*

> This document is drafted inside the cli-wrapper workspace for convenience, but
> Security Guard Agent is a **standalone project** with its own repository,
> release cadence, and license. cli-wrapper is only its first dogfood target.

---

## Overview

**Security Guard Agent** is a Claude Code plugin that watches code being
written by other agents (Claude Code, subagents, or third-party LLM agents
that share a Claude Code session) and flags security risks in real time. It
combines **deterministic AST-based pattern detection** with **LLM-based
semantic reasoning**, delivered as a single installable plugin that bundles
hooks, a subagent, a skill, and an MCP server.

The guiding principle: **deterministic work belongs in code, reasoning
belongs in the LLM**. Tree-sitter finds the obvious bugs; the subagent
decides whether they matter and how to fix them.

### Goals

1. Flag high-signal security issues in code as soon as another agent writes it,
   without requiring the coding agent to ask.
2. Let any other agent (including the Claude Code main loop) invoke a
   security review on demand over a clean tool interface.
3. Keep detection rules **extensible by prompt** for semantic checks and
   **extensible by code** for deterministic checks, so both LLM-native and
   engineering-native contributors can add rules.
4. Ship as a single plugin install — one command, everything wired up.
5. Produce findings that are machine-readable (SARIF) and human-readable
   (Markdown), suitable for both CI gating and interactive review.

### Non-Goals (v1)

- Replacing CodeQL, Semgrep, or Snyk. This is a **triage tool**, not a
  complete SAST suite.
- Guaranteed soundness. False negatives are acceptable if false-positive
  rates stay low enough to keep the agent trustworthy.
- Windows-only features. Plugin targets macOS and Linux first (same as
  Claude Code's primary platforms).
- Runtime protection (WAF, RASP). Static analysis only.
- Remediation without human approval. Findings are reports, not auto-fixes.

### Primary User Stories

1. *As a developer using Claude Code, I install `codeguard` once. From that
   point on, whenever Claude edits a file, I see a short security report in
   the terminal if anything looks risky.*
2. *As an agent in a multi-agent workflow, I finish writing a module and
   want a second opinion. I call `mcp__codeguard__scan_file(path)` and get
   back a structured list of findings.*
3. *As a release engineer, I run `/codeguard audit` before cutting a
   release. It runs the full scan across the repository plus the
   OpenSSF-Scorecard-style repository hygiene checks and writes a SARIF
   file to the build output directory.*

---

## Section 1 — Delivery Form

### 1.1 The Hybrid: Plugin + Bundled MCP Server

Security Guard Agent ships as **one Claude Code plugin** whose `plugin.json`
manifest declares every component it needs. This is supported by Claude Code
as of the current plugin manifest schema, which lets a single plugin bundle
`commands`, `agents`, `skills`, `hooks`, and `mcpServers` together.

```
┌────────────── Claude Code Plugin: codeguard ──────────────┐
│                                                            │
│  .claude-plugin/plugin.json                                │
│                                                            │
│  hooks/                                                    │
│    PostToolUse-scan.sh        ← Edit|Write|MultiEdit       │
│    PostToolUse-mcp.sh         ← mcp__codeguard__.*         │
│                                                            │
│  agents/                                                   │
│    security-guard.md          ← LLM brain                  │
│                                                            │
│  skills/                                                   │
│    security-rules/                                         │
│      SKILL.md                 ← knowledge base             │
│      references/              ← per-category deep dives    │
│        injection.md                                        │
│        authz.md                                            │
│        crypto.md                                           │
│        ...                                                 │
│                                                            │
│  mcp/                                                      │
│    codeguard-engine/          ← deterministic sensors      │
│      scan_file                                             │
│      scan_diff                                             │
│      scan_secrets                                          │
│      audit_scorecard                                       │
│      parse_ast                                             │
│                                                            │
└────────────────────────────────────────────────────────────┘
```

### 1.2 Distribution: Marketplace

The plugin is distributed through a standard Claude Code plugin marketplace
(a public git repository with a `marketplace.json` pointing at the plugin).
Installation is a single flow:

```
/plugin marketplace add github.com/<owner>/codeguard-marketplace
/plugin install codeguard
```

This one install registers **all** of the plugin's components at once:
commands, agents, skills, hooks, and the MCP server. No manual
`settings.json` editing is required from the user. `/reload-plugins` cycles
the whole bundle if the plugin is updated in place.

### 1.3 Why This Form Was Chosen

Alternatives considered and rejected:

- **Plugin alone, no MCP**: hook scripts would have to shell out to custom
  binaries on every invocation, and there would be no clean way for peer
  agents (subagents in the same session, or Cursor/Cline users) to call the
  engine by name. Loses portability.
- **MCP server alone, no plugin**: no standard way to auto-trigger on
  `Edit|Write|MultiEdit`. The user would have to wire a hook manually in
  `settings.json`, defeating the one-command install promise.
- **Agent SDK app, standalone**: best isolation, but the integration cost
  into a host agent session is significant (IPC, lifecycle management), and
  there is no marketplace distribution story.

The hybrid captures the strengths of both primitives: **hooks give us
automatic triggering**, **MCP gives us a language-agnostic tool interface**,
and the plugin manifest fuses them into a single installable unit.

---

## Section 2 — Responsibility Split (Option C: Balanced Hybrid Detection)

Detection responsibilities are split deliberately across four components.
Each component has one job, a well-defined interface, and can be tested in
isolation. This is the **Option C / Balanced Hybrid Detection** split.

### 2.1 Decision Matrix: Who Detects What

| Category                         | Detection class          | Primary owner  | Reason |
|----------------------------------|--------------------------|----------------|--------|
| Hardcoded credentials / secrets  | Pattern + entropy        | **MCP**        | Regex + Shannon entropy; no context needed |
| SQL injection (obvious)          | AST + sink catalog       | **MCP**        | Detect raw string concat into `db.Query`, `exec.SQL`, etc. |
| SQL injection (semantic)         | Context-sensitive        | **SubAgent**   | Taint flows across function boundaries require reasoning |
| Command injection                | AST + sink catalog       | **MCP**        | Detect raw string into `exec.Command`, `os/exec`, shell |
| Path traversal (`../`)           | AST + taint pattern      | **MCP**        | Detect unchecked user input reaching `os.Open`, `filepath.Join` |
| Insecure random (`math/rand`)    | AST import check         | **MCP**        | 1-line AST query; deterministic |
| TLS MinVersion unset             | AST config check         | **MCP**        | Look for `tls.Config{}` without `MinVersion:` field |
| Weak crypto (`md5`, `sha1`, DES) | AST import + call check  | **MCP**        | Import list + function-call shape |
| Unsafe deserialization           | AST + library catalog    | **MCP+Agent**  | MCP flags sinks (`gob.Decode`, `yaml.Unmarshal` into `interface{}`); SubAgent confirms source is untrusted |
| **Missing authorization checks** | **Semantic**             | **SubAgent**   | Requires understanding route handlers, middleware chains, business context |
| **Privilege / TOCTOU races**     | **Semantic**             | **SubAgent**   | Requires temporal reasoning across calls |
| **Business-logic flaws**         | **Semantic**             | **SubAgent**   | Requires domain understanding |
| **False-positive filtering**     | **Any finding**          | **SubAgent**   | LLM re-reads context and drops obvious FPs before reporting |
| **Fix suggestions**              | **Any finding**          | **SubAgent**   | LLM writes the remediation diff or description |
| OpenSSF-Scorecard checks         | API + config inspection  | **MCP**        | 100% deterministic — GitHub REST calls + file parsing |

### 2.2 MCP — Deterministic Sensors

The MCP server is a pure-Go binary (tree-sitter bindings) that exposes these
tools. All tools are side-effect free except `audit_scorecard`, which reads
GitHub REST APIs.

```
Tool                     Input                              Output
────────────────────────────────────────────────────────────────────
scan_file                path                               Finding[]
scan_diff                diff_text | base_ref               Finding[]
scan_secrets             path | glob                        Finding[]
parse_ast                path, language                     ASTHandle
query_ast                ASTHandle, tree-sitter query       Node[]
audit_scorecard          repo_url, checks[]                 ScorecardReport
get_rules                category?                          Rule[]

type Finding struct {
    ID          string   // stable, e.g. "CG-SQLI-001"
    Severity    string   // critical | high | medium | low | info
    Category    string   // "sql-injection", "hardcoded-secret", ...
    File        string
    Line        int
    Column      int
    Snippet     string   // the offending code fragment
    Rule        string   // rule that fired
    Confidence  string   // high | medium | low (for FP triage)
    Deterministic bool   // true if rule has no LLM step
}
```

Rationale for keeping this layer deterministic:

1. **Fast**: Tree-sitter parses ~1 MB/sec; finding scan latency stays
   well under a second even for multi-thousand-line files.
2. **Reproducible**: Same input, same output. CI caching works.
3. **Token-cheap**: Findings are small JSON; the LLM never sees whole
   files unless it asks.
4. **Composable**: Other tools (VS Code extensions, CI shell scripts) can
   call the MCP server directly without going through the LLM.

### 2.3 SubAgent — LLM Brain

The `security-guard` subagent sits between raw MCP findings and the user.
Its job is:

1. **Triage**: re-read each finding in context and drop false positives.
   This alone moves precision from ~60% (typical pattern-match output) to
   ~90%+ based on prior art with LLM-assisted SAST.
2. **Rank**: assign human-meaningful priority (not just severity) — e.g. a
   hardcoded test fixture is lower priority than the same pattern in
   production code, even if the regex score is identical.
3. **Explain**: translate each surviving finding into a one-paragraph
   explanation of *why* it is dangerous.
4. **Suggest**: produce a fix proposal (diff or prose) the user can review.
5. **Cover semantic gaps**: for categories marked **SubAgent** in §2.1
   (missing authz, TOCTOU, business-logic flaws), the agent directly reads
   the file and reasons without a prior MCP finding.

The subagent uses:
- MCP tools from §2.2 (to parse ASTs, fetch related files, check config).
- Claude Code's built-in tools (`Read`, `Grep`, `Glob`) for cross-file
  context.
- The skill under `skills/security-rules/` as its rule knowledge base.

The subagent is invoked by the hook (automatic path) or by a peer agent
calling the `Agent` tool (manual path).

### 2.4 Skill — Rule Knowledge Base

`skills/security-rules/SKILL.md` is the progressive-disclosure entry point
that lists all rule categories and when to load each. Deep-dive references
live under `skills/security-rules/references/<category>.md`:

- `references/injection.md` — SQL / command / template injection taxonomy,
  sink catalogs, typical fix patterns.
- `references/authz.md` — missing check idioms, middleware chain patterns,
  BOLA/BFLA detection heuristics.
- `references/crypto.md` — weak primitives, TLS config, HMAC misuse.
- `references/secrets.md` — common secret patterns, entropy thresholds,
  test-fixture allow-listing.
- `references/deserialization.md` — per-language unsafe deserialization
  sinks and their safe alternatives.
- `references/scorecard.md` — mapping of OpenSSF Scorecard checks to
  concrete evidence Claude Code can inspect.

When the subagent investigates a finding, it loads only the relevant
reference file, not the whole rule set. This keeps per-review token usage
predictable.

**Extensibility model**: adding a new semantic rule is a prompt edit in a
markdown file. Adding a new deterministic rule is a Go source change in the
MCP server. The split is intentional: rule authors choose the cheaper path
for the rule.

### 2.5 Hooks — Nervous System

Two hook scripts live under `hooks/`. Both are tiny shims that call the MCP
server and then invoke the subagent.

**2.5.1 `PostToolUse-scan.sh`** — matcher `Edit|Write|MultiEdit`

Fires every time an agent modifies a file. Flow:

```
PostToolUse(Edit|Write|MultiEdit)
    ↓
hook reads the tool_input (file path) from stdin (hook JSON payload)
    ↓
calls mcp__codeguard__scan_file(path)
    ↓
if len(findings) == 0:  exit 0  (silent)
    ↓
invoke security-guard subagent with findings + path
    ↓
subagent writes a short report to stdout (hook's stdout is shown to user)
```

This gives us the *"Claude wrote a file, Claude should also look at what it
wrote"* loop for free. The hook exits fast when there is nothing to report,
so the common case is zero friction.

**2.5.2 `PostToolUse-mcp.sh`** — matcher `mcp__codeguard__add_.*|mcp__codeguard__update_.*|mcp__codeguard__delete_.*`

Reserved for the secondary trigger the user asked about: when any agent uses
an MCP tool that mutates data inside the codeguard engine (e.g. staging new
files for review). Fires a follow-up scan on the changed data. This is the
"closest possible" approximation to "MCP data change → subagent" — we
cannot react to MCP resource-update notifications directly, so we react to
the *tool call* that caused the change.

### 2.6 Repository Hygiene (OpenSSF Scorecard) — Separate Trigger

The Scorecard-style checks (`Branch-Protection`, `Signed-Releases`,
`Dependency-Update-Tool`, `SAST`, `CII-Best-Practices`, `Pinned-Dependencies`,
`Token-Permissions`, `Vulnerabilities`, `Code-Review`, `Maintained`) do not
belong in the per-file hook loop — they are repository-level, slow, and
require network calls to GitHub. They live in their own path:

- **Trigger**: on demand via a slash command (`/codeguard audit`) and
  optionally via a cron/scheduled hook.
- **Implementation**: `mcp__codeguard__audit_scorecard` does the GitHub REST
  calls and returns a structured `ScorecardReport`. The subagent then writes
  a Markdown summary.
- **Not wired into `PostToolUse`**: keeps the per-edit hook loop fast and
  local.

---

## Section 3 — Data Flows

### 3.1 Automatic Flow (hook-triggered)

```
Other agent edits file.go
        ↓
Claude Code executes Edit tool
        ↓
Edit completes successfully
        ↓
PostToolUse hook fires (matcher matches)
        ↓
hooks/PostToolUse-scan.sh reads hook JSON from stdin
        ↓
shell calls: mcp__codeguard__scan_file("file.go")
        ↓
MCP server:
    - parses AST via tree-sitter
    - runs deterministic rule catalog
    - returns Finding[] (possibly empty)
        ↓
if empty → exit 0, silent
        ↓
otherwise shell invokes security-guard subagent
    with { findings, file_content, context }
        ↓
subagent:
    - loads skills/security-rules/SKILL.md
    - triages each finding (FP filter)
    - loads relevant references/<category>.md per surviving finding
    - adds semantic-only findings (authz, TOCTOU, etc.)
    - ranks and explains
    - emits Markdown report
        ↓
hook stdout → visible in Claude Code session
```

### 3.2 Manual Flow (peer agent invocation)

```
Peer agent (subagent or main loop) decides to ask for a review
        ↓
calls Agent tool: security-guard
    with { task: "review file.go for auth issues" }
        ↓
security-guard subagent starts
        ↓
uses MCP tools + Read/Grep/Glob for context
        ↓
returns findings report to caller
```

### 3.3 Audit Flow (on-demand repo scan)

```
User runs /codeguard audit
        ↓
command script invokes security-guard subagent with task "full audit"
        ↓
subagent calls in parallel:
    - mcp__codeguard__scan_diff(base=HEAD~100)  ← code scan
    - mcp__codeguard__audit_scorecard(repo)     ← hygiene scan
    - mcp__codeguard__scan_secrets(glob="**/*") ← secret scan
        ↓
subagent merges, triages, writes a single report
        ↓
report saved to .codeguard/reports/YYYY-MM-DD.md
report also written as SARIF to .codeguard/reports/YYYY-MM-DD.sarif
```

---

## Section 4 — Technology Choices

| Layer       | Choice                                         | Reason |
|-------------|------------------------------------------------|--------|
| Plugin shell| Markdown + JSON manifest (Claude Code native)  | Native, marketplace-ready |
| Hooks       | POSIX shell + `jq`                             | Minimal deps, portable on macOS/Linux |
| MCP server  | **Go**, tree-sitter-go bindings                | Fastest AST parsing, single static binary, easy cross-compile for the marketplace |
| Languages supported (v1) | Go, Python, TypeScript/JavaScript | Cover most LLM-written code; each has a tree-sitter grammar |
| SubAgent model | Claude Sonnet 4.6 default; Opus 4.6 opt-in for `/codeguard audit` | Cost/latency balance; Opus for deep audits |
| Report format | Markdown (human) + SARIF 2.1.0 (machine) | SARIF = industry standard, GitHub Code Scanning compatible |
| Marketplace | Public git repo with `marketplace.json`        | Standard Claude Code distribution |

**Deliberate non-choices**:

- No database. Findings are transient per invocation; reports are plain
  files on disk. The state store is git.
- No web UI. All output is CLI/Markdown. A future v2 could add one, not v1.
- No language runtime beyond Go. The MCP server is one static binary per
  platform, distributed via GoReleaser.

---

## Section 5 — Security Considerations for the Agent Itself

A security tool that is itself insecure is worse than no tool at all. The
agent must:

1. **Never execute scanned code.** Only parse and read. No `exec`, no
   dynamic evaluation, no plugin loading from scanned sources.
2. **Respect path boundaries.** Scans are limited to the current workspace.
   The MCP server refuses absolute paths outside the repository root
   unless explicitly passed via a `--allow-external` flag.
3. **Sandbox AST parsing.** Tree-sitter is memory-safe but CPU-greedy;
   each scan has a 10-second wall-clock timeout to defend against
   pathological inputs.
4. **No telemetry.** The plugin does not phone home. Findings stay local.
5. **GitHub token scope minimization.** `audit_scorecard` uses only the
   token's read scopes; it refuses to run if the token has write
   permissions unless `--i-know-what-im-doing` is set.
6. **Supply chain.** Plugin releases are signed with cosign; MCP server
   binaries are built reproducibly via GoReleaser; dependencies are pinned
   by commit SHA in CI (dogfooding the Scorecard `Pinned-Dependencies`
   check on itself).

---

## Section 6 — Error Handling & Failure Modes

| Failure                                         | Behavior |
|-------------------------------------------------|----------|
| MCP server crash mid-scan                       | Hook exits 0 with a stderr warning; does not block the edit |
| Tree-sitter parse failure (malformed source)    | Treated as "no findings"; logged to `.codeguard/logs/` |
| Subagent returns garbled output                 | Hook falls back to printing raw MCP findings |
| LLM API rate limit                              | Subagent degrades: returns deterministic findings only, flags that LLM triage was skipped |
| GitHub API rate limit in `audit_scorecard`      | Partial report with explicit "skipped: rate-limited" entries |
| Hook timeout (>30s)                             | Hook aborts; subagent task is canceled; user sees warning |
| User has no network                             | `scan_file`/`scan_diff`/`scan_secrets` work offline; `audit_scorecard` returns a clear "offline" error |

**Core invariant**: no failure of the security guard ever blocks the
underlying edit or halts the user's development flow. The guard is
**advisory**, not **gating**. If it fails, the worst case is silent
degradation.

---

## Section 7 — Testing Strategy

| Layer                | Test type                                      |
|----------------------|------------------------------------------------|
| MCP tool contracts   | Unit tests per tool with golden-file fixtures  |
| Deterministic rules  | Corpus of synthetic vulnerable/safe files per rule; tracked precision/recall |
| Tree-sitter queries  | Per-language snippet tests (Go, Python, TS)    |
| Subagent triage      | Replay tests: canned MCP outputs → expected subagent decisions, asserted over a prompt-stable sample |
| Hook integration     | End-to-end: invoke Claude Code in a fixture repo, run `Edit`, assert hook output |
| Scorecard checks     | Mocked GitHub REST responses                    |
| SARIF output         | Schema-validate against SARIF 2.1.0 JSON schema |
| Performance          | Benchmark `scan_file` on 100 KB / 1 MB files; fail CI if > 2× regression |
| Supply chain         | Self-audit: `codeguard` runs on `codeguard` in CI |

A **bug-bounty corpus** of real-world disclosed CVEs (in the supported
languages) is the primary acceptance gate. v1 target: detect at least 70%
of the corpus at high or critical severity with precision ≥ 85%.

---

## Section 8 — Open Questions

These are deferred to the implementation plan and do not block spec
approval, but they should be resolved before code is written.

1. **Project name**. `codeguard` is a placeholder. Alternatives to
   consider: `sentinel`, `vigil`, `warden`, `aegis`. Subject to name
   availability on GitHub and npm.
2. **License**. Default: Apache-2.0 (to match cli-wrapper). Confirm there
   are no dependencies that would force AGPL.
3. **Per-language support priority**. Current order: Go → Python →
   TypeScript/JavaScript. Confirm this matches the user's actual use case
   or reorder.
4. **Baseline model**. Sonnet 4.6 proposed for the hook path. Confirm
   token budget expectations; Haiku 4.5 is the fallback for cost-sensitive
   users.
5. **SARIF output path**. Default `.codeguard/reports/`. Configurable?
6. **`.codeguardignore`**. Should the tool honor an ignore file for
   known-acceptable findings? Probably yes; format TBD (YAML vs plain
   glob).
7. **Cross-tool handoff**. Should findings be emitable as GitHub Code
   Scanning alerts via `gh api`? Low-cost addon if the SARIF is already
   there.

---

## Section 9 — Scope Boundary for This Spec

This spec describes **one coherent plugin**. It is intentionally sized for
**one implementation plan**. If any of the following are requested later,
they should become their own specs:

- IDE integrations (VS Code extension) — separate spec.
- Runtime / dynamic analysis — separate spec, likely different project.
- Multi-repo / organization-wide dashboards — separate spec.
- Auto-remediation (the guard writes fixes directly) — separate spec,
  requires a very different trust model.

---

## Appendix A — Plugin Manifest Sketch

A rough, illustrative `plugin.json`. Exact field names may need adjustment
against the latest Claude Code plugin schema at implementation time.

```json
{
  "name": "codeguard",
  "version": "0.1.0",
  "description": "LLM-assisted security guard for agentic coding",
  "author": "mhha",
  "license": "Apache-2.0",
  "repository": "https://github.com/<owner>/codeguard",
  "keywords": ["security", "sast", "claude-code", "mcp"],

  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Edit|Write|MultiEdit",
        "command": "${CLAUDE_PLUGIN_ROOT}/hooks/PostToolUse-scan.sh"
      },
      {
        "matcher": "mcp__codeguard__(add|update|delete)_.*",
        "command": "${CLAUDE_PLUGIN_ROOT}/hooks/PostToolUse-mcp.sh"
      }
    ]
  },

  "agents": [
    "agents/security-guard.md"
  ],

  "skills": [
    "skills/security-rules/"
  ],

  "commands": [
    "commands/codeguard-audit.md"
  ],

  "mcpServers": {
    "codeguard-engine": {
      "command": "${CLAUDE_PLUGIN_ROOT}/mcp/codeguard-engine",
      "args": []
    }
  }
}
```

---

## Appendix B — Reference Prior Art

- **CodeQL (GitHub/Semmle)** — taint-analysis inspiration; *not* replaced
  by this tool. CodeQL remains the gold standard for exhaustive analysis;
  `codeguard` targets the *"fast feedback during agentic coding"* niche
  instead.
- **OpenSSF Scorecard** — the repository hygiene check set is a direct
  mapping target for `audit_scorecard`.
- **Semgrep** — precedent for LLM-assisted rule expansion and SARIF output.
- **tree-sitter** — upstream AST engine; the MCP server is essentially a
  security-rule layer on top of tree-sitter queries.
