---
name: media-stack-test
description: Verify homelab Media Stack changes with focused Go tests during TDD and a compact full-suite check.
---

# Test the Media Stack

Run from the repo root. The runner at `.agents/skills/media-stack-test/scripts/test-go` takes `go test` arguments. It keeps the full output in a temporary log and reports test results, skips, and the Go exit status in a few lines.

1. Find the nearest test with `rg` in the changed `internal/<package>` for package behavior or `tests/acceptance` for CLI and rendered configuration behavior. The fixture acceptance tests invoke the CLI from source. Use `go test <package> -list '<pattern>'` if you need to confirm top-level test names.
2. Run the same focused command for red and green. Red needs the expected behavior assertion. Green needs a passing result for the intended test. Check the selection if the runner reports no tests or a skip.
   ```sh
   .agents/skills/media-stack-test/scripts/test-go ./tests/acceptance -run '^TestPlanRendersCheckedInImagesForSelectedEnvironment$' -count=1
   ```
3. On failure, read the relevant log excerpt. Locate a named assertion with `rg -n -m 3 -F -- '--- FAIL: TestName' <log>` and inspect nearby lines with `sed`. For build or setup failures, check the start of the log. Read the log before rerunning for more detail.
4. After the focused loop, run the full suite once with `.agents/skills/media-stack-test/scripts/test-go ./...` (the repo's `go test ./...`). Use `.github/workflows/test.yaml` to check prerequisites if a fixture fails on a missing tool.

Keep `MEDIA_STACK_LIVE_*` opt-ins off for the local loop. When verification requires a real pinned service, read the relevant `*_live_test.go` for its opt-in and prerequisites, then run that test against its authorized disposable environment.
