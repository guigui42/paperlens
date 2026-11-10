# Paperlens implementation plan

## Problem and approach

Build a separate, Google-like search frontend for an existing Paperless-ngx
archive. Users describe the document they remember instead of learning Paperless
query syntax. Copilot SDK expands the query, retrieves candidates through the
Paperless API, and ranks them using document evidence. Paperless remains the
source of truth for documents, OCR, metadata, permissions, and ingestion. V1 uses
its existing search index, not a second document store, embedding service, or
vector database.

## Confirmed decisions

- Application: Paperlens, in public repository `guigui42/paperlens`.
- Go, `html/template`, HTMX 4, and Alpine CSP.
- Search-first landing page and ranked results, not a chatbot.
- Household shared archive, LAN/VPN only, with no application login.
- Dedicated Paperless integration account with intended shared-document access.
- Server-configured Copilot identity, not per-user OAuth. Questions and relevant
  excerpts may reach the configured Copilot model.
- Live retrieval, query expansion, and reranking. No separate semantic index.
- Preview, download, open in Paperless, and manual upload.
- Out of scope: edits to existing documents, deletes, tagging, archive migration,
  scanner control, replacing ingestion, and live deployment.

## Reference constraints

[scan-hub's web UI](https://github.com/guigui42/scan-hub/blob/main/README.md#web-ui)
uses Go `html/template`, embedded HTMX 4.0.1 and Alpine CSP 3.17.5, without a
frontend build or CDN. Use verified compatible versions, not HTMX 2 assumptions.
Jobs start bounded background Go work and HTMX polls HTML fragments. Use a Host
allowlist, same-origin and CSRF guards, strict self-only CSP, external JS/CSS.

[Paperless's API](https://github.com/paperless-ngx/paperless-ngx/blob/main/docs/api.md)
provides full-text search scores/highlights and asynchronous uploads. Check
contracts against the configured version, with Paperless 3.3/API v10 as the
reference target.

[Copilot Go requires a CLI runtime](https://github.com/github/copilot-sdk/blob/main/docs/setup/local-cli.md):
pin a compatible SDK/runtime pair using an explicit path or supported bundler.
[Authentication](https://github.com/github/copilot-sdk/blob/main/docs/auth/authenticate.md)
must verify entitlement and permitted use. Token acceptance alone does not
establish that subscription sharing is permitted.

Search must account for French/English/accent variants, title/filename relevance
ahead of incidental OCR for "latest" queries, and mentioned year versus issue date.
Preserve the `Export financier` tag exclusion: those documents' content and
metadata never enter LLM prompts, tools, or transcripts, but remain searchable as
clearly labelled non-AI results.

## User experience

### Landing and results

Minimal branding, prominent search field and submit, secondary upload, fictional
example prompts, ordinary French/English queries, keyboard/focus/accessibility
and mobile support. Explain discreetly that selected excerpts go to the model.
No AI calls while typing. Warm-light/charcoal theme, restrained rose accent and
shared CSS variables. Alpine handles local state; HTMX handles server interaction.

Keep the search field on results pages. Show interpreted intent and editable
date, correspondent, document-type, tag, and custom-field filters, including
person if defined. Show canonical titles, useful metadata, matching excerpts,
and evidence-backed relevance explanations. Relevance/newest intent must not
place newer incidental OCR above strong title/type matches.

Use same-origin safe PDF/image preview, original download, and direct Paperless
links. Unsupported formats fall back to download. Show real retrieval stages
through polling, not invented percentages or reasoning. Cancellation, empty,
partial, degraded, and error states must be explicit. Label ordinary keyword
fallback and AI-excluded documents. Use stable snapshot pagination. Report unique
candidate counts honestly rather than summing overlapping searches.

### Manual upload

File picker/drop area plus optional title, through Paperless ingestion, OCR, and
classification. Confirm supported formats for the configured instance; validate
extension, type, size, and filename rather than assuming optional parsers exist.
Distinguish uploading, accepted/processing, consumed, duplicate, and failed.
Expose a document ID only after task success. Keep bounded restart-safe task
receipts; promptly remove temporary file data. Never blindly retry an ambiguous
non-idempotent upload. Upload is the only writing path and never an agent tool.

## Search pipeline

1. Direct original-query search plus a short-lived metadata vocabulary cache,
   without copying or repeatedly listing the whole archive.
2. Copilot gets scoped `lookup_metadata`, `search_documents`, and
   `read_document_excerpt` tools for focused synonyms/searches/supported filters.
3. Explicit UI hard constraints are immutable in Go. The model cannot remove
   person/date/other filters; empty results never silently relax constraints.
4. Deduplicate IDs and use rank-based merging across queries rather than
   uncalibrated raw scores. Preserve title/filename/metadata signals.
5. Apply AI exclusions before any model payload. Supply bounded eligible
   excerpts with IDs.
6. Require strict reranking output. Resolve titles, URLs, dates, and actions
   server-side, never from model-supplied URLs.
7. Validate IDs against retrieved accessible candidates and quotes against
   supplied text. Invalid/fabricated results cause explicit AI failure and
   labelled keyword fallback.
8. Put strongest evidence first; label weak matches as uncertain.

Initial configurable safeguards: at most six Paperless search calls per AI job,
100 unique candidates, 12 excerpt-bearing documents, and 8,000 characters per
document. Finite deadline, bounded global concurrency, and small queue. Test
exhaustion, cancellation, and busy behavior. Mark limits rather than implying
exhaustive results. Missing OCR or no keyword/metadata overlap can miss results;
evaluate semantic recall rather than promising it.

## Backend boundaries

- `cmd/paperlens`: configuration, dependencies, HTTP lifecycle, health command.
- `internal/paperless`: typed REST, metadata/search, preview/download, uploads/tasks.
- `internal/search`: retrieval, merging, budgets, exclusions, validation.
- `internal/copilot`: SDK lifecycle, restricted tools, auth/model, session cleanup.
- `internal/web`: handlers, templates, embedded assets, security, browser jobs.
- `internal/config`: validated environment/file secrets.

Use small interfaces at real boundaries. No extra framework, broker, database,
or OCR service. Persist only app keys and bounded upload receipts, not an archive
replica or search history.

### Paperless integration

Probe version/schema and supported pinned API. Do not assume unreleased upstream
main matches the deployed server. Support pagination, typed metadata/custom
fields/highlights, and explicit authentication errors. Bind URLs, redirects,
next links, and binary proxies to the configured origin. Bound size, deadline,
and contexts. Keep the token server-side. Document dedicated least-privilege
read/upload/metadata/task permissions and object access, never superuser reuse.
Escape OCR/highlights or safely rebuild them; never inject upstream HTML.

### Copilot isolation

Explicit tool allowlist and deny-by-default permissions. No shell, filesystem,
web, arbitrary HTTP, MCP, memory, or delegation. App-owned isolated home/work
directory prevents ambient skills, plugins, instructions, repositories,
credentials, and MCP discovery. OCR is data, and Go enforces policy.

Isolated search sessions, no shared conversation; cancellation/expiry, explicit
session deletion, and verified on-disk CLI transcript/log cleanup. Disconnect
alone is insufficient. Logs contain only redacted counts, durations, correlation
IDs, never prompts/text/answers. Explain remote provider processing separately
from local cleanup. Support environment/file secrets and actionable
missing/expired/unauthorized errors.

### Web boundary

All network-reachable users can see the shared account's documents. Require and
document LAN/VPN access, never public exposure. Host allowlist, origin and CSRF
checks on search/cancel/upload, limits, safe headers, strict Alpine-compatible
CSP. Cookies scope jobs/receipts, not authentication. Jobs outlive individual
polls but are bounded by cancellation, expiry, shutdown, and concurrency.

## Implementation checklist

- [ ] Establish repository/runtime; prove pinned SDK/CLI with synthetic input and restricted tools.
- [ ] Typed Paperless integration, configuration, capability checks, fake server.
- [ ] Copilot retrieval/ranking/exclusion/budget/validation/cancellation/fallback.
- [ ] Search/document UI, filters, progress, stable pages, previews/downloads/errors.
- [ ] Upload validation, task tracking, restart-safe receipts, navigation.
- [ ] Isolation/cleanup/concurrency/network/CSRF/CSP/log/public-data hardening.
- [ ] Contract, race, browser, adversarial, and relevance validation.
- [ ] Tested non-root Docker/runtime, Compose/env, fake demo, operation docs. No live deployment.

## Acceptance criteria

Deterministic `httptest` contracts and fake agent for CI. Cover pagination,
metadata IDs, custom fields, schema differences, authentication, rate limits,
timeouts, redirects, binaries, task outcomes, variants/languages/accents/latest,
hard filters, mentioned year, deduplication, weak/no matches, budget exhaustion,
and malformed output.

Adversarial OCR cannot enable tools, invent links, bypass filters, or leak
excluded exports. Inspect runtime files/logs too. Browser tests use fake services
only and cover keyboard/mobile/progress/cancel/refinement/preview/download/upload,
errors, and degraded mode.

At least 24 labelled answerable and six no-match synthetic relevance requests.
Compare original Paperless rank to the full pipeline. Target at least 90%
Hit@3; every displayed ID/quote must be grounded. Real SDK/model synthetic
evaluations run outside ordinary CI. Fake-model checks do not prove AI quality.
Record model, budgets, and outcomes.

Run gofmt, `go vet`, configured lint, and relevant `go test -race`; aim for at
least 80% integration/search coverage without shallow tests. Docker smoke tests
cover pinned CLI, restricted tools, fake Paperless, health/readiness, non-root
user, writable directories, cancellation, and cleanup.

Public content must exclude real documents, OCR, names, private URLs, tokens,
runtime sessions, and unredacted screenshots. Deliver a standalone local/Docker
app with full search/preview/download/upload and documentation of authentication,
LAN boundary, AI exclusions, data flow, usage, retrieval limits, and recovery.
Report exact unverified boundaries where credentials or environment prevent
live SDK/model/Docker validation. Supply a reviewable synthetic demo or saved
screenshots. Do not deploy the household stack.
