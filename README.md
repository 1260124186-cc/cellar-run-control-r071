# Cellar Run Control

Cellar Run Control is a local Go HTTP service for coordinating a small
production cellar. It keeps formula approval, vessel availability, active
fermentation runs, gravity observations, conditioning, completion metrics, and
vessel cleaning in one deterministic process.

This repository is a healthy initialization baseline. It has no intentional
defects and no automated test suite. Tests are intentionally deferred to a
later engineering task; the production workflow checks in this repository
remain available for immediate build and behavior verification.

## Requirements

- Go 1.23 or later
- A writable local data directory
- No database, network service, or third-party module

## Build And Run

Build every package:

```bash
go build ./...
```

Start the service:

```bash
go run ./cmd/fermentctl serve
```

The default address is `127.0.0.1:8088`. The default snapshot directory is
`./data`.

Environment variables:

- `CELLAR_CONTROL_ADDR`: HTTP listen address.
- `CELLAR_CONTROL_DATA_DIR`: directory containing `state.json`.

Example:

```bash
CELLAR_CONTROL_ADDR=127.0.0.1:9099 \
CELLAR_CONTROL_DATA_DIR=/tmp/cellar-control \
go run ./cmd/fermentctl serve
```

## Directory Structure

```text
cmd/fermentctl                 service and workflow-check executable
internal/clock                 time boundary
internal/config                environment configuration
internal/domain                entities, tags, validation, transitions, metrics
internal/httpapi               HTTP routes, JSON decoding, errors
internal/search                rebuildable generationed search index
internal/service               use-case orchestration
internal/storage               revisioned snapshot persistence with commit hooks
internal/workflowcheck         real HTTP production checks
```

## Public API

Health:

- `GET /healthz`

Formula workflow:

- `POST /v1/formulas`
- `GET /v1/formulas`
- `GET /v1/formulas/{id}`
- `PUT /v1/formulas/{id}/steps`
- `POST /v1/formulas/{id}/approve`
- `POST /v1/formulas/{id}/retire`

Vessel workflow:

- `POST /v1/vessels`
- `GET /v1/vessels`
- `GET /v1/vessels/{id}`
- `POST /v1/vessels/{id}/finish-cleaning`
- `POST /v1/vessels/{id}/retire`

Run workflow:

- `POST /v1/runs`
- `GET /v1/runs`
- `GET /v1/runs/{id}`
- `POST /v1/runs/{id}/reserve`
- `POST /v1/runs/{id}/start`
- `POST /v1/runs/{id}/observations`
- `POST /v1/runs/{id}/conditioning`
- `POST /v1/runs/{id}/complete`
- `POST /v1/runs/{id}/abort`

Search index:

- `GET /v1/search`
- `POST /v1/search/rebuild`
- `GET /v1/search/status`
- `GET /v1/search/tags`

Formulas, vessels, and runs accept an optional lowercase `tags` array (at
most 8, each 1–32 lowercase letters, digits, or internal hyphens) on
creation. Tags are part of the searchable text and the tag filter.

### Search

`GET /v1/search` covers formula names and styles, vessel codes, run codes,
and tags. Query parameters are combined with AND semantics:

- `q`: free text; whitespace separates words, each of which must match.
  Codes match with or without hyphens (`fv100` finds `FV-100`).
- `kind`: `formula`, `vessel`, or `run`; repeatable / comma-separated.
- `state`: repeatable / comma-separated; validated against the selected
  kinds, so a vessel code and a run state can be narrowed simultaneously.
- `vessel_code`: exact vessel code; for runs it matches the vessel the run
  currently occupies, resolved from the live snapshot.
- `tag`: repeatable; every listed tag must be present on one entity.
- `include_archived`: set `true` to include retired formulas and vessels and
  aborted runs. Archived entities are hidden by default.
- `limit`: 1–100, default 25.
- `cursor`: opaque continuation token returned as `next_cursor`.

Results carry a `ref` (`kind`, `id`, `path`) that links straight to the
detail endpoint, e.g. `/v1/vessels/vessel_...`. Ordering is stable: most
recently updated first, then kind, then ID. Each page is pinned to one
immutable index generation carried inside the cursor; an expired generation
returns `410 cursor_expired`, so the client restarts without a cursor.

The index is fully derived from the snapshot and holds no truth of its own:
every hit is re-resolved against the current committed snapshot before it is
returned, so an index that lags during heavy writes, a rebuild interrupted by
restart, or an archived entity can never surface an object that no longer
exists, and run results can never inherit another vessel's facts. Writes
notify the engine asynchronously (they never block on indexing); the engine
coalesces notifications and also supports a synchronous forced rebuild via
`POST /v1/search/rebuild`.

Writes accept an optional `X-Expected-Version` header. Errors use a stable
shape:

```json
{
  "error": {
    "code": "revision_conflict",
    "detail": "the aggregate version does not match X-Expected-Version",
    "field": ""
  }
}
```

## Workflow Checks

Run each production workflow check separately:

```bash
go run ./cmd/fermentctl check formula-approval
go run ./cmd/fermentctl check vessel-run-lifecycle
go run ./cmd/fermentctl check observations-completion
go run ./cmd/fermentctl check search-index
```

Each check creates a temporary snapshot directory, starts the real HTTP
handler on a loopback listener, issues JSON requests, verifies success and
failure behavior, and removes its temporary data.

## Persistence And Recovery

The service stores a complete revisioned snapshot at
`<data-directory>/state.json`. A write creates a temporary file in the same
directory, commits the encoded bytes durably, closes the handle, and renames
the result over the previous snapshot. The in-memory snapshot is replaced only
after that operation succeeds.

On startup, an existing snapshot is decoded and normalized. Unknown or
conflicting map indexes are not silently invented; invalid JSON or a negative
revision stops the process before HTTP serving begins.

## Repair-Ready Boundaries

The baseline intentionally keeps several independent behavior surfaces visible
for later engineering evaluation:

- formula name normalization and duplicate detection;
- target gravity and process-step range validation;
- aggregate version conflict checks;
- atomic vessel reservation and release;
- run state transition guards;
- observation sequence and time ordering;
- gravity movement and range checks;
- metric calculation and rounding;
- durable snapshot replacement and startup normalization.

These are production mechanisms, not injected defects. A later task can add
automated tests or request a focused repair without changing the public API.

## Test Boundary

This stage does not create unit tests, integration tests, test fixtures, or an
E2E suite. The later test task owns systematic coverage of validation limits,
state transitions, concurrency, persistence failures, and numeric edge cases.

## Scope

The service does not control physical equipment, authenticate remote users,
send notifications, expose a browser interface, or replicate data across
processes. It is intended for a trusted local network or an internal adapter.
