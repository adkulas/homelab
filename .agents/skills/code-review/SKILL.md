---
name: code-review
description: Review the changes since a fixed point (commit, branch, tag, or merge-base) along two axes — Standards (does the code follow this repo's documented coding standards?) and Spec (does the code match what the originating issue/spec asked for?). Runs both reviews in parallel sub-agents and reports them side by side. Use when the user wants to review a branch, a PR, work-in-progress changes, or asks to "review since X".
---

Two-axis review of the diff between `HEAD` and a fixed point the user supplies:

- **Standards** — does the code conform to this repo's documented coding standards?
- **Spec** — does the code faithfully implement the originating issue / spec?

Both axes run as **parallel sub-agents** so they don't pollute each other's context, then this skill aggregates their findings.

The issue tracker should have been provided to you — run `/setup-matt-pocock-skills` if `docs/agents/issue-tracker.md` is missing.

## Process

### 1. Pin the fixed point

Whatever the user said is the fixed point — a commit SHA, branch name, tag, `main`, `HEAD~5`, etc. If they didn't specify one, ask for it.

Capture the diff command once: `git diff <fixed-point>...HEAD` (three-dot, so the comparison is against the merge-base). Also note the list of commits via `git log <fixed-point>..HEAD --oneline`.

Before going further, confirm the fixed point resolves (`git rev-parse <fixed-point>`) and the diff is non-empty. A bad ref or empty diff should fail here — not inside two parallel sub-agents.

### 2. Identify the spec source

Look for the originating spec, in this order:

1. Issue references in commit messages (`#123`, `Closes #45`, GitLab `!67`, etc.). Record the stable reference; let the Spec sub-agent fetch its full contents through `docs/agents/issue-tracker.md`.
2. A path the user passed as an argument.
3. A spec file under `docs/`, `specs/`, or `.scratch/` matching the branch name or feature.
4. If nothing is found, ask the user where the spec is. If they say there isn't one, the **Spec** sub-agent will skip and report "no spec available".

### 3. Identify the standards sources

Anything in the repo that documents how code should be written, such as `CODING_STANDARDS.md` or `CONTRIBUTING.md`.

The Standards axis also applies [the Fowler smell baseline](SMELL-BASELINE.md). The Standards sub-agent must read that file. Repository standards override it; its smells are always judgement calls, and tooling-enforced findings are omitted.

### 4. Spawn both sub-agents in parallel

Spawn each with `fork_turns: "none"`. Their prompts are self-contained indexes: include only commands and source paths or stable issue references, and instruct the agent to retrieve those sources itself.

**Standards sub-agent prompt** — include:

- The diff command and commit list.
- The standards-source paths and `.agents/skills/code-review/SMELL-BASELINE.md`.
- The brief: "Read every named standard and the smell baseline. Report per file/hunk: (a) documented-standard violations with file + rule; and (b) baseline smells, named and quoted. Distinguish hard violations from judgement calls; repository standards override the baseline. Skip tooling-enforced findings. Under 400 words."

**Spec sub-agent prompt** — include:

- The diff command and commit list.
- The spec path or stable tracker reference plus `docs/agents/issue-tracker.md` when fetching is required.
- The brief: "Read the complete spec, then report: (a) missing or partial requirements; (b) scope creep; and (c) apparently implemented requirements whose implementation is wrong. Quote the spec line for each finding. Under 400 words."

With no spec, run Standards alone and report "no spec available." Reuse the same agents for finding-only follow-ups after fixes, sending only the relevant commit and prior findings.

### 5. Aggregate

Present the two reports under `## Standards` and `## Spec` headings, verbatim or lightly cleaned. Do **not** merge or rerank findings — the two axes are deliberately separate (see _Why two axes_).

End with a one-line summary: total findings per axis, and the worst issue _within each axis_ (if any). Don't pick a single winner across axes — that's the reranking the separation exists to prevent.

## Why two axes

A change can pass one axis and fail the other:

- Code that follows every standard but implements the wrong thing → **Standards pass, Spec fail.**
- Code that does exactly what the issue asked but breaks the project's conventions → **Spec pass, Standards fail.**

Reporting them separately stops one axis from masking the other.
