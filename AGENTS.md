# AGENTS.md

## Rules

- **NEVER push to git or create commits without explicit user permission.** The user will say "push", "commit", or "запуш" when ready.
- After making code changes, verify with `go build` and `go vet` before reporting done.
- Run `make build-all` to verify the project compiles.

## Testing

- `./test-e2e.sh` — full end-to-end test: builds all binaries, starts server + nginx + client, runs loadtest, cleans up on exit (including Ctrl-C).
- Manual: `make test-nginx` to start nginx on :40443, `make stop-nginx` to stop.
- Loadtest env vars: `N` (requests, default 100), `C` (concurrency, default 20), `PROXY` (default 127.0.0.1:9999).
