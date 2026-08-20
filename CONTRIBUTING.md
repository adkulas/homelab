# Contributing

## Fresh local setup

This repository does not require a prebuilt release binary for local development.
Build the Go CLI from source and run it directly from the repo root:

```bash
go build -o bin/media-stack ./cmd/media-stack
./bin/media-stack init --environment staging
```

If you want to use the launcher instead, `setup.sh` expects a published GitHub Release asset and will download the pinned
binary before running `media-stack init`. For local development, the source build path above is the supported first step. 

## Recommended first loop

1. Edit `stacks/media/media-stack.yaml` so the `production` and `staging` environments have absolute, non-overlapping
   `dataRoot` values and distinct LAN ports.
2. Build the CLI locally with `go build -o bin/media-stack ./cmd/media-stack`.
3. Run `./bin/media-stack init --environment staging` and complete the prompts.
4. Follow with `./bin/media-stack doctor --environment staging` and `./bin/media-stack plan --environment staging`.
5. Run the full test suite with `./bin/media-stack test` or `go test ./...` before opening a pull request.

## Notes

- Use the local build for day-to-day development and debugging.
- Use `setup.sh` only when you want the pinned release-binary bootstrap path.
- Run `./bin/media-stack test` when you want the repo's full test suite from the CLI, or `go test ./...` from the repository root.
- Every future pull request must keep this document aligned with the current developer workflow and test commands.
- Keep generated binaries out of version control.
