# Validation record

All fixture documents and browser artifacts are fictional. No household archive,
household token, or private repository data was used.

## Verified locally

- Go 1.27.3 compilation, deterministic contracts, `go vet`, configured
  golangci-lint 2.15.0, and race-detector tests.
- Paperless 3.3/API v10 contract source checked for version headers, query/filter
  parameters, metadata pagination, upload UUIDs, and paginated ingestion tasks.
  HTTP fixtures cover denied authentication, rate limiting, redirects,
  malformed/bounded responses, filters, upload multipart data and task IDs.
- Search fixtures cover exclusions before AI, unretrieved/fabricated IDs,
  quote validation, immutable hard filters, date intent, accent folding,
  call/excerpt budgets, deduplication, fallback, cancellation and strict JSON.
- Browser-scoped jobs and receipts, same-origin/Host/CSRF checks, safe rendering,
  preview/download, upload MIME validation, duplicate handling, restart recovery
  and bounded queues.
- 16 Playwright checks pass using installed Chrome, with desktop and mobile
  emulation. Actual vendored HTMX 4 and Alpine CSP run under the application's
  CSP. Checks include keyboard search, theme, filters, cancellation, errors,
  empty/excluded results, preview/download and upload outcomes.
- The checksum-verified CLI release v1.0.105 works with Go SDK v1.0.28 for
  start/ping, restricted-session creation, actual runtime tool-list inspection,
  disconnect, explicit session deletion, process stop and directory cleanup.
  Only the three application tools are offered. No inference credential was
  supplied to this lifecycle check.

## Synthetic relevance result

| Measure | Result |
|---|---|
| Answerable cases | 24 |
| No-match cases | 6 |
| Original synthetic lexical Hit@3 | 24/24 (100%) |
| Fake-agent pipeline Hit@3 | 24/24 (100%) |
| Correct no-match decisions | 6/6 |
| AI failures hidden as successful evaluation | 0 |

This is a deterministic contract check, **not an actual model evaluation** and
not evidence of an improvement over Paperless. Both baseline and fake-agent
pipeline meet the threshold on these fixtures. The report includes limits and
elapsed time. Run `go run ./cmd/evaluate -live` with an explicitly configured
server identity to evaluate actual inference on synthetic data.

## Unverified boundaries

- Live Copilot authentication, entitlement, model relevance and adversarial
  model behavior: no server identity was supplied. No ambient user credentials
  were reused. The configurable default `gpt-5.4` follows the pinned SDK setup
  example and is checked against the runtime model list before inference.
- Live Paperless deployment behavior: only versioned upstream source and
  deterministic HTTP contracts were used. Connect a dedicated integration
  identity in a controlled environment before relying on production retrieval.
- Local Docker execution: Docker CLI is installed but the local daemon is
  unavailable. The Dockerfile and non-root smoke script are provided; container
  execution must pass in CI or on a machine with a running Docker daemon.
- Real household search recall: requires a separately authorized evaluation.
  This implementation intentionally does not query private documents.

## Reproducible runtime check

The SDK's pinned metadata names CLI release `v1.0.105`. The verified macOS and
Linux artifacts report `1.0.105` with isolated homes. An initial version check
without isolation resolved an ambient cached runtime instead, which is why the
application supplies its own home, configuration, work and cache directories.
Release artifacts are also pinned by SHA-256.

```sh
PAPERLENS_TEST_CLI=/absolute/path/to/pinned/copilot \
  go test -v ./internal/copilot -run TestPinnedRuntimeLifecycle -count=1
```

The browser installer stalled after downloading Chromium in this environment.
It was stopped, and the full suite was run successfully through Playwright's
supported installed-Chrome channel. No application behavior was stubbed in the
browser.
