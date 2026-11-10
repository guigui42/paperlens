# Validation record

All fixture documents and published browser artifacts are fictional or blank
landing pages. Private deployment checks and credentials are kept out of this
public record. Hosted relevance evaluations use only the synthetic archive.

## Verified locally

- Go 1.27.3 compilation, deterministic contracts, `go vet`, configured
  golangci-lint 2.15.0, and race-detector tests.
- Paperless 3.3 and 3.4.2/API v10 contract source checked for version headers, query/filter
  parameters, metadata pagination, upload UUIDs, and paginated ingestion tasks.
  HTTP fixtures cover denied authentication, rate limiting, redirects,
  malformed/bounded responses, filters, upload multipart data and task IDs.
- Search fixtures cover exclusions before AI, unretrieved/fabricated IDs,
  quote validation, immutable hard filters, date intent, accent folding,
  call/excerpt budgets, deduplication, fallback, cancellation and strict JSON.
- Browser-scoped jobs and receipts, same-origin/Host/CSRF checks, safe rendering,
  preview/download, upload MIME validation, duplicate handling, restart recovery
  and bounded queues.
- Runtime-root ownership, atomic browser-key publication, force-stop on failed
  runtime shutdown, one-call candidate reuse, normalized OCR source offsets,
  late cancellation, error-target preservation and polling synchronization have
  focused regression coverage. The approved Lax cookie policy is checked with
  a synthetic external-dashboard receipt navigation; Origin/CSRF rejection
  coverage remains unchanged.
- 16 Playwright checks pass using installed Chrome, with desktop and mobile
  emulation. Actual vendored HTMX 4 and Alpine CSP run under the application's
  CSP. Checks include keyboard search, theme, filters, cancellation, errors,
  empty/excluded results, preview/download and upload outcomes.
- The current checksum-verified CLI release v1.0.109 works with Go SDK v1.0.31 for
  start/ping, restricted-session creation, actual runtime tool-list inspection,
  application-tool invocation, forbidden-tool rejection, disconnect, explicit
  session deletion, process stop and directory cleanup.
  Only the three application tools are offered. No inference credential was
  supplied to this lifecycle check.

The current pair is the latest stable SDK/CLI release checked on 2026-11-10.
[SDK v1.0.31's runtime dependency](https://github.com/github/copilot-sdk/blob/v1.0.31/nodejs/package.json)
explicitly names CLI 1.0.109. Session creation now requests manual permission mode
and refuses inference unless the runtime confirms that mode. Restricted tool
enumeration, allowed/forbidden tool execution, cancellation, process shutdown and
local-state cleanup were rechecked with the new pair. Runtime process-file
logging is explicitly disabled in addition to the existing telemetry controls.

The initial deterministic race-test coverage was 89.3% for the Paperless integration and
85.9% for search. Web handlers are 76.1%; configuration is 90.9%. CLI-dependent
checks run separately from the ordinary test suite, so ordinary SDK-adapter
coverage is not a claim that the credential-dependent inference path was tested.

## Linux and container validation

[Current CI runs](https://github.com/guigui42/paperlens/actions/workflows/ci.yml)
track the Go/race/vet/lint/evaluation/browser job and the Linux amd64 container
job. Select the run whose head commit matches the revision under review rather
than treating a historical run as evidence for newer code. The container job
builds the image, checks UID 10001, uses a read-only root,
checks the pinned CLI, runs the restricted SDK smoke command, checks health and
readiness, and verifies that the per-search runtime directory is empty afterward.

The first container runs exposed two real packaging constraints, both fixed:
the CLI needs a writable isolated home, and its extracted native addon cannot
load from a `noexec` tmpfs. The final configuration gives the runtime a dedicated
`exec` tmpfs while upload temporary storage remains on a separate `noexec` mount.

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

## Hosted synthetic relevance result

A subsequent real hosted evaluation used `gpt-6.1-sol` through the then-pinned
SDK 1.0.28/CLI 1.0.105 pair. It completed all 30 cases in 454.18 seconds:

| Measure | Result |
|---|---|
| Answerable Hit@3 | 24/24 (100%) |
| Correct no-match decisions | 6/6 |
| Keyword fallbacks | 0 |
| Original synthetic lexical Hit@3 | 24/24 (100%) |

This run used a real model, not the deterministic fake agent. IDs and quotes were
validated by the production search engine. Limits remained six search calls,
100 unique candidates, 12 excerpts and 8,000 characters per excerpt. The runtime
directory was empty afterward, including session transcripts and logs.

The fixture baseline also scores 100%, so this result does not demonstrate
improvement over a real Paperless index or measure private-archive recall.

The latest stable pair, **SDK 1.0.31 / CLI 1.0.109**, was then evaluated with the
same `gpt-6.1-sol` model and all 30 synthetic cases. It scored 24/24 answerable
Hit@3, 6/6 correct no-match decisions and zero fallbacks in 485.18 seconds.
Manual permission mode was confirmed before inference, and the isolated runtime
directory was empty afterward. This is a new hosted run, not a relabelled result
from the previous runtime pair; the same synthetic-baseline limitations apply.

## In-app WebKit origin regression

The real in-app browser reproduced a rejected HTMX submission at the correct
canonical origin. With HTMX 4's `mode: same-origin` fetch and
`Referrer-Policy: no-referrer`, WebKit sent `Origin: null`, no Referer, and
`Sec-Fetch-Site: same-origin`. Host matched the configured origin.

`Referrer-Policy: same-origin` fixes the legitimate request without admitting
opaque or foreign origins and still suppresses cross-origin referrers. The
actual in-app frontend now passes search/poll and cancellation. An empty-file
upload form reaches backend validation and is rejected before any Paperless
upload. Regression tests retain strict Origin, Host and CSRF checks; normal
Playwright runs also assert the browser policy header.

## Review regression coverage

Additional synthetic tests cover missing/renamed/partially resolved exclusion
policies, normalized names, unknown tag metadata, and rechecking the policy after
metadata changes. They assert that the agent is never called when the policy
cannot be resolved.

Teardown tests distinguish recoverable session RPC failures from unconfirmed
runtime shutdown or state removal. They check redacted stage diagnostics, the
distinct disabled-AI notice, a bounded unresponsive disconnect, and cancellation
cleanup against the actual pinned CLI without credentials or inference.

Metadata tests cover a cancelled waiter during network refresh, coalesced
concurrent refreshes, and refusal to reuse expired metadata after failure.
HTTP/job tests cover stable labels without poll-time metadata calls, huge/invalid
page numbers, completed-history eviction, capacity retained during cancellation
cleanup, shutdown admission rejection, and full-page non-HTMX upload navigation.

## Unverified boundaries

- Model behavior outside the 30 synthetic cases, exhaustive prompt-injection
  resistance and relevance on private archives are not established by this run.
  Each deployment must verify its own identity, entitlement and model policy.
- Live ingestion was intentionally not exercised by uploading a real file.
  Upload outcome/restart/duplicate handling is covered by deterministic fixtures.
- Release images are smoke-tested on native Linux amd64 and arm64 in Actions.
  Local Docker ARM64 smoke tests additionally verify the exact bundled runtime.
  These credential-free checks establish runtime and container behavior, not
  private-archive relevance or a user's model entitlement.
- Real household search recall requires a separately authorized evaluation.
  No private archive documents or URLs are included in fixtures or artifacts.

## Reproducible runtime check

The SDK's pinned metadata names CLI release `v1.0.109`. The verified macOS and
Linux artifacts report `1.0.109` with isolated homes. An initial historical check
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
