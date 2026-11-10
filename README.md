# Paperlens

A search-first frontend for Paperless-ngx. Describe the document you remember,
refine the search with hard filters, and open the evidence behind each result.
Go, `html/template`, HTMX 4, Alpine CSP, and the GitHub Copilot SDK. No frontend
build, CDN, vector database, archive replica, or chatbot.

**LAN/VPN only. There is no login. Every visitor can access documents visible to
the configured Paperless account.** Host checks and browser cookies are not
authentication. Do not expose this service publicly.

The browser policy is `Referrer-Policy: same-origin`: it sends no referrer to
other origins while keeping same-origin fetches compatible with WebKit's
`Origin` handling. Explicit missing, opaque (`null`) and foreign origins are
still rejected on every mutation, even when Referer or fetch-metadata headers
claim same-origin. A valid CSRF token is required separately.

## Try the fictional archive

Requires Go 1.27. No Paperless service, Copilot credentials, or model usage:

```sh
go mod download
PAPERLENS_DEMO=true go run ./cmd/paperlens
```

Open [localhost:8080](http://localhost:8080). Try “latest home insurance
certificate”, “facture électricité septembre”, or “washing machine warranty”.
Demo mode is visibly labelled and uses keyword retrieval, not a fake AI claim.
Demo uploads are in memory and disappear when the process restarts.

## What works

- Submitted-query search with staged progress, cancellation, bounded queues,
  explicit keyword-only mode and labelled AI fallback.
- Live Paperless retrieval, French/English query expansion through Copilot,
  rank-based deduplication, bounded excerpts and strictly validated result IDs
  and quotes. Titles, dates, actions and URLs always come from Paperless.
- Immutable issue-date, correspondent, type, tag and scalar custom-field
  filters, including a Person field if the archive defines one. Text, select,
  date, boolean, integer, float and URL fields support exact matching. Select
  options are listed with their labels. Monetary and document-link fields are
  deliberately not offered as scalar filters.
- PDF and PNG/JPEG previews through a same-origin, MIME-checked proxy. Archived
  PDFs are used when Paperless provides them. Other formats offer download/open
  in Paperless. The proxy has a 32 MiB limit.
- Manual single-file uploads with type/extension/size checks and restart-safe
  ingestion receipts. Uploads are never an AI tool.
- Keyboard and mobile layouts, light/dark theme, local vendored scripts and a
  CSP without `unsafe-inline` or `unsafe-eval`.

No existing-document edits, deletes, tagging, scanner control or deployment of
your Paperless stack.

## Connect your archive

1. Create a **dedicated, non-superuser Paperless identity**. Grant view access to
   documents and metadata (tags, correspondents, document types, custom fields),
   add-document permission for manual upload, and view-task permission. Grant
   object-level access only to the intended shared archive. Do not reuse your
   administrator token. Paperless v10 restricts non-staff task visibility to the
   identity's own tasks and unowned tasks.
2. Verify the LAN/VPN or authenticated reverse-proxy boundary. Set one canonical
   origin with the exact scheme, hostname and port. The proxy must preserve that
   Host. Paperlens does not trust forwarded Host headers.
3. Configure the token through an environment variable or a mounted secret file.
   The app reads exported environment variables, not `.env` files automatically.
4. Confirm the configured instance's installed ingestion parsers before setting
   `PAPERLENS_UPLOAD_TYPES`. If unset, uploads are disabled. The allowlist supports
   PDF, PNG, JPEG and TIFF; optional Office/HTML parsers are not assumed.

Keyword-only example, with entirely fictional configuration:

```sh
export PAPERLENS_LAN_ONLY=true
export PAPERLENS_ORIGIN=http://localhost:8080
export PAPERLESS_URL=http://paperless.example:8000
export PAPERLESS_TOKEN_FILE=/absolute/path/to/paperless_token
export PAPERLENS_AI=false
export PAPERLENS_UPLOAD_TYPES=pdf,png,jpg,jpeg
go run ./cmd/paperlens
```

The integration deliberately checks **Paperless 3.3.x or 3.4.2, API v10** and the actual
OpenAPI filter contract at startup. `X-Version` is the application version;
`X-Api-Version` is the API version; OpenAPI `info.version` is independently
`6.0.0` in the pinned source. Versioned schema responses may qualify it as
`6.0.0 (10)`. Only those two exact revisions are accepted, and every required
filter is still checked. A different contract fails closed rather than
silently dropping filters.

Metadata pagination is rebuilt from the expected next page number and fixed
page size against the configured origin. If a reverse proxy advertises HTTP
links for that same host and metadata route, Paperlens still sends HTTPS when
HTTPS is configured. Other hosts, routes, credentials, query parameters, page
jumps and redirects remain rejected.

References: [Paperless API](https://docs.paperless-ngx.com/api/),
[3.3 filter definitions](https://github.com/paperless-ngx/paperless-ngx/blob/v3.3.0/src/documents/filters.py),
[3.4.2 filter definitions](https://github.com/paperless-ngx/paperless-ngx/blob/v3.4.2/src/documents/filters.py),
[v10 task serializer](https://github.com/paperless-ngx/paperless-ngx/blob/v3.3.0/src/documents/serialisers.py),
[version headers](https://github.com/paperless-ngx/paperless-ngx/blob/v3.3.0/src/paperless/middleware.py).

## Enable Copilot

Paperlens uses a server-configured identity. Before enabling a shared interface,
verify that the identity has access to Copilot and the chosen model, and that
your intended usage complies with the applicable subscription and organization
policies. **Successful token authentication is not proof that sharing a
subscription through an app is permitted.** See the
[SDK authentication guide](https://github.com/github/copilot-sdk/blob/v1.0.28/docs/auth/authenticate.md).

```sh
export PAPERLENS_AI=true
export COPILOT_GITHUB_TOKEN_FILE=/absolute/path/to/copilot_token
export COPILOT_CLI_PATH=/absolute/path/to/pinned/copilot
export COPILOT_MODEL=gpt-5.4
go run ./cmd/paperlens
```

`COPILOT_GITHUB_TOKEN` is also supported, but cannot be combined with its `_FILE`
setting. Paperlens does **not** reuse ambient `gh` credentials, your normal
Copilot home, repository instructions or installed MCP servers.

### Pinned runtime

| Component | Pin |
|---|---|
| Go SDK | `github.com/github/copilot-sdk/go v1.0.28` |
| CLI release artifact | `github/copilot-cli v1.0.105`, SHA-256 verified |
| CLI version in isolated homes | `1.0.105` on macOS and Linux |
| Default model | `gpt-5.4`, configurable and checked against the identity's model list |
| HTMX | 4.0.1 |
| Alpine CSP | 3.17.5 |

The CLI release pin comes from the
[SDK's own `copilotCliVersion`](https://github.com/github/copilot-sdk/blob/v1.0.28/nodejs/package.json).
Check the version with an isolated home: the release launcher can otherwise use
an ambient cached runtime. Do not silently substitute an arbitrary installed
CLI. Download the matching platform artifact and verify
it against [the release checksums](https://github.com/github/copilot-cli/releases/tag/v1.0.105).
Docker includes the Linux runtime with fixed checksums. Go's binary alone is not
sufficient. See [SDK local runtime setup](https://github.com/github/copilot-sdk/blob/v1.0.28/docs/setup/local-cli.md).

Check the installed pair without credentials or model inference:

```sh
COPILOT_CLI_PATH=/absolute/path/to/pinned/copilot go run ./cmd/paperlens runtime-check
```

This verifies the SDK handshake, actual runtime tool list, session deletion and
local runtime cleanup. It does not validate authentication or model quality.

## Data flow and limits

The original query is searched in Paperless first. Copilot can then call only
`lookup_metadata`, `search_documents`, and `read_document_excerpt`. Go enforces
all hard filters, budgets and exclusions, regardless of model output.

By default, documents tagged **`Export financier`** never enter model tool results, prompts
or transcripts. They remain visible as explicitly labelled, non-AI keyword
results. Keep that tag on the documents you want excluded. The policy uses
metadata observed for the retrieval snapshot, not an archive-wide content scan.

`PAPERLENS_EXCLUDED_TAGS` can specify a comma-separated replacement list of
required privacy-tag names; it defaults to `Export financier`. Keep the
financial-export tag in that list unless you explicitly rename the policy.
Names match case/accent-insensitively with surrounding/repeated whitespace
normalized. Every required name must resolve. Missing or renamed tags produce
an explicit keyword fallback without calling the model, with a warning on the
landing page and at startup. The policy is rechecked for each search against
the current metadata snapshot. Documents containing unknown tag IDs are also
kept out of AI until metadata is refreshed.

Searches are not exhaustive. Missing OCR, an absent lexical overlap, truncated
Paperless pages, and the configured budgets can all hide a relevant document.
The UI reports limits rather than inventing a total across overlapping queries.
An exact supported quote is necessary for an AI result, but evidence grounding
does not guarantee that the model's relevance judgement is correct.

| Bound | Default / maximum |
|---|---|
| Paperless search calls per job | 6 / 6, including the original query |
| Unique candidates | 100 / 100 |
| Excerpt-bearing documents | 12 / 12 |
| Characters per excerpt | 8,000 / 8,000 |
| Search deadline | 90 seconds / 3 minutes |
| Concurrent searches | 2 / 8 |
| Additional queued searches | 4 / 16 |
| In-memory search snapshots | 64, kept up to 10 minutes; oldest completed snapshots evicted when full |
| Upload file size | 20 MiB / 100 MiB |
| Concurrent uploads | 2 |
| Upload receipts | 200, expire after 24 hours |
| Metadata cache | 5 minutes, at most 2,000 items per vocabulary; one bounded refresh at a time |

Metadata refreshes do not hold the cache mutex during network requests, and
waiting callers can cancel independently. Failed refreshes never silently
return expired privacy metadata. Result polling uses the vocabulary captured
with its search snapshot, so it performs no metadata requests and labels remain
stable. Completed history cannot block new searches while execution capacity is
available. Invalid or oversized page numbers explicitly show the first page.

Recent/newest intent protects strong title/filename relevance from newer
incidental OCR matches. A year mentioned in a query is not automatically
converted into an issue-date filter.

Each AI search gets a private runtime directory and conversation. Empty-mode
defaults, an explicit allowlist, runtime tool-list verification, pre-tool hooks
and denied permission requests prohibit shell/filesystem/web/MCP/delegation.
Sessions are disconnected **and deleted**, the process is stopped, and its
directory is removed. Cancellation also requests abort. Recoverable session RPC
teardown errors are logged by stage without raw error payloads and affect only
that search when runtime stop and state removal both succeed. Unconfirmed
runtime shutdown, a forced cleanup timeout, or failed local-state removal still
disables AI until restart, with a distinct user-facing notice. The pinned SDK
does not provide typed process-exit confirmation for an errored `Stop`, so such
errors remain fail-closed rather than being guessed harmless. A dedicated
runtime root is cleaned of app-named
orphan searches at startup.

Only the signing key and bounded receipts persist in app storage. Queries,
document snippets and results live in memory until snapshot expiry; no search
history is written. Logs do not include prompts/OCR/model answers or upstream
error bodies. The CLI's telemetry is disabled. Local cleanup does **not** revoke
data already processed by GitHub/model providers; review their applicable data
handling terms separately. For disk-backed local runs, deleted files are not a
secure erase. Docker uses a tmpfs runtime directory.

## Upload recovery

`POST /api/documents/post_document/` accepts a file and returns a task UUID. HTTP
200 is **not** a stored document. Paperlens polls the v10 paginated task response,
using `status`, `result_data.duplicate_of` and `related_document_ids`.

- **Accepted/processing:** keep the receipt link or revisit Upload in the same
  browser. Closing the page does not resubmit the file.
- **Stored:** a successful task provided an actual document ID.
- **Duplicate/failed:** no new stored document is claimed.
- **Unknown:** a connection failed after dispatch, a restart interrupted
  dispatch, or Paperless returned an unrecognized result. Check Paperless's task
  list and archive **before uploading again**.

Reservations are fsynced before dispatch, repeated submissions reuse the receipt,
and upload forms expire with their receipt window. No non-idempotent request is
automatically retried. Temporary multipart files are removed after the request.
Receipts store browser scope, task/status/ID/timestamps, not filenames or OCR.
Deleting browser cookies loses the receipt association, not the document.
Without HTMX, valid submissions redirect to a full receipt page and validation
errors retain a complete upload page with a fresh form nonce.

## Docker / Compose

For released images and later Komodo deployment, use
[compose.release.yaml](compose.release.yaml) and the digest-pinned environment
asset from [GitHub Releases](https://github.com/guigui42/paperlens/releases).
The image includes the pinned Copilot CLI; the Docker host needs no CLI install.
See [Docker releases and Komodo](docs/docker-release.md) for registry access,
secret files, updates, rollback and the Actions publication workflow.

For a local source build, see [compose.yaml](compose.yaml) and
[.env.example](.env.example). Copy the example
environment, supply `.secrets/paperless_token` and `.secrets/copilot_token`
locally, and protect those files. Container UID/GID 10001 must be able to read
mounted secret files. Do not commit them.

```sh
docker compose build
docker compose up -d
docker compose logs --tail=50
```

The example publishes only loopback, drops capabilities, uses a read-only root
filesystem, runs non-root, persists receipts/keys in `/data`, and uses tmpfs for
runtime state. `/runtime` needs the `exec` mount option because the pinned CLI
extracts and loads its native addon there. Upload temporary storage remains on
the separate `noexec` `/tmp` mount. This permits the trusted runtime's native
library loading, not model shell/filesystem tools. Put a LAN/VPN reverse proxy
in front if needed, preserving the
configured Host. The example is not a household-stack deployment.

`/healthz` checks the HTTP process. `/readyz` checks access to the metadata
vocabulary (subject to its short cache). Copilot failures degrade search rather
than taking keyword search offline.

## Development and validation

```sh
go test -race -coverprofile=coverage.out ./...
go vet ./...
golangci-lint run                 # config targets v2.15.0
go run ./cmd/evaluate             # fake-agent contract evaluation only
npm ci                           # browser-test dependency only
npx playwright install chromium
npm run test:browser
bash scripts/smoke-docker.sh
```

For an already installed Chrome, use
`PAPERLENS_BROWSER_CHANNEL=chrome npm run test:browser`.

The deterministic relevance corpus has 24 answerable and six no-match prompts.
It compares original **synthetic lexical** ranking with the complete pipeline.
The fake agent is not proof of AI performance or of real Paperless search recall.

An explicit live evaluation uses the same fictional archive and incurs model
usage. It never connects to `PAPERLESS_URL`:

```sh
PAPERLENS_DEMO=true \
  COPILOT_GITHUB_TOKEN_FILE=/absolute/path/to/copilot_token \
  COPILOT_CLI_PATH=/absolute/path/to/pinned/copilot \
  go run ./cmd/evaluate -live
```

See [validation results and remaining boundaries](docs/validation.md) and the
[approved implementation plan](plan.md). UI prior art:
[scan-hub](https://github.com/guigui42/scan-hub#web-ui).
