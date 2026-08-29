---
name: implement
description: "Implement a piece of work based on a spec or set of tickets."
disable-model-invocation: true
---

Implement the work described by the user in the spec or tickets.

Capture the starting `HEAD` before editing; it is the fixed point for the final review.

## Tight loop

1. Resolve the authoritative spec, then turn its acceptance criteria into a compact behavior matrix: public seam, required behavior, source of truth, and observable evidence. Resolve ownership, lifecycle, sensitivity, and vocabulary questions before encoding metadata or interfaces.
2. Inspect narrowly. Locate candidates with `rg -l` or focused `rg -n`, then read only the matching functions and required context. Use `jq` to print only the fields needed from large JSON or generated output. After one confirmed tool-infrastructure failure, use one scoped fallback instead of retrying the same operation.
3. Use /tdd at the pre-agreed seams. Work in vertical slices and run only the affected test or package after each slice. Run formatting and typechecking regularly; successful focused commands should stay quiet.
4. Before review, audit the implementation against every row of the behavior matrix. Search for stale vocabulary, generic lifecycle/ownership defaults, implementation-symbol leakage, and missing symmetric coverage.
5. Create a candidate commit, then use /code-review against the captured starting `HEAD`. Apply findings, run affected tests, amend the candidate, and ask the existing reviewers to verify only their findings until both axes are clean.
6. Run the full test suite once on the post-review state, then hand off that exact tested tree unchanged.

Commit the tested work to the current branch.
