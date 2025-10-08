# Repository Guidelines

## Project Structure & Module Organization
CLI entrypoint lives in `cmd/starget/main.go` and wires Cobra commands into the downloader core. Shared logic and types sit in `stargzget/` alongside matching `_test.go` files; extend features here before exposing new flags. Lightweight logging utilities live in `logger/logger.go`, and `output/` contains sample extraction trees that double as fixtures when validating manual runs. Strategic docs like `DESIGN.md` and `ROADMAP.md` capture architecture and upcoming work—keep them in sync with code-level changes.

## Build, Test, and Development Commands
- `go build -o starget ./cmd/starget` — compile the CLI binary; run before shipping changes touching Cobra commands.
- `go run ./cmd/starget --help` — verify new flag wiring or usage text updates.
- `go test ./...` — execute all unit tests; add `-cover` when preparing reports.

## Coding Style & Naming Conventions
- Format Go sources with `gofmt` (tabs for indentation, goimports-style grouping); run `go fmt ./...` before committing.
- Keep packages short and focused; exported symbols use CamelCase, internal helpers stay lowerCamel.
- Name tests `TestThing_Scenario` to mirror functions or behaviors under test.
- Route user-facing logs through `logger` so verbosity switches (`--verbose`, `--debug`) behave consistently.

## Testing Guidelines
- Write table-driven tests in `stargzget/*.go` to cover edge cases such as missing blobs or credential errors.
- Use temporary directories (`t.TempDir()`) instead of committing new fixtures; existing `output/` data is for manual sanity checks only.
- Target ≥80% coverage on touched packages with `go test ./stargzget -coverprofile=coverage.out` and inspect via `go tool cover -func`.

## Commit & Pull Request Guidelines
Recent history favors short, lowercase, imperative messages (e.g., `refactor the download work`). Keep the first line under ~65 characters and follow with optional detail paragraphs separated by a blank line. Pull requests should outline the problem, summarize the solution, link relevant roadmap issues, and attach CLI output or screenshots whenever behavior changes.

## Security & Configuration Tips
Use the `--credential` flag for registries that need auth, but load secrets from environment variables or `os.Stdin` rather than hardcoding them. Never commit real credentials or personal image references; sanitize logs and fixtures before sharing.
