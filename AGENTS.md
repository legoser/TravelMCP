# AGENTS.md

## Project

`travelmcp` is a Go MCP server for multimodal public-transport route planning.
The server accepts origin/destination points and search parameters, then builds
door-to-door itineraries (bus, tram, train, flight, and walking legs) using the
CSA planner over a canonical provider network.

## Source of Truth

This file contains stable project constraints only. Phase-specific details belong
in `docs/` and must not be duplicated here.

* `docs/00-index.md` is the entry point to the current documentation and plan.
* Before an architecture-level change (new package, changed `Store`/`Provider`
  interface, new external integration, data-model change), read the current plan
  in `docs/`.
* If this file conflicts with the current plan, the plan is authoritative and
  this file should be updated to restore consistency.

## Technology

* Go module: `travelmcp`; toolchain requirements are defined by `go.mod`.
* `github.com/mark3labs/mcp-go`: MCP protocol, Streamable HTTP, JSON-RPC 2.0,
  stateless transport (`WithStateLess`), API-key authentication.
* `gopkg.in/yaml.v3`: configuration.
* PostgreSQL + PostGIS: target production store; PostGIS is used for geographic
  data such as `places` and `terminals`.
* External paid APIs are not used; only open data sources are allowed.
* Add a new dependency only when the standard library and existing dependencies
  do not reasonably cover the requirement.

## Architecture Invariants

* The canonical domain model lives only in `internal/model` and has no external
  dependencies. Adapters convert external data into the canonical model instead
  of defining duplicate domain types.
* Dependency direction is downward:
  `cmd` → `server` → `mcp`/`planner` → `providers`/`store`/`geo` → `model`.
* Upper layers must not depend on `server`, `mcp`, or `cmd`.
* External I/O (HTTP, files, database) belongs behind adapters and the designated
  data-access layer, not directly in business logic.
* Keep responsibilities isolated. Prefer small, consumer-focused interfaces;
  implementations of an interface must remain interchangeable; upper layers
  depend on interfaces rather than concrete database or transport types.
* Use `make check-layers` for dependency-boundary validation.
* Remove obsolete implementations as the migration plan progresses. After a
  completed migration step, run `make check-deprecated`.
* Identifiers, codes, and enum-like values use Latin characters. User-facing
  names, error text, and MCP tool descriptions remain in Russian.
* Configuration precedence is strictly `default → YAML → env`. Secrets must
  come from environment variables and must not be committed.
* Behavioral parameters such as thresholds, limits, and weights must have one
  authoritative configuration location.

## Domain Invariants

* New data enters the canonical model only through the common pipeline:
  collection → normalization → enrichment → deduplication → verification →
  canonicalization.
* Manually confirmed data must not be silently overwritten by automatic
  re-verification. Conflicts require review.
* Quota-limited external APIs must use the shared database-backed quota counter,
  not process-local state.
* Database migrations are additive by default. Do not use destructive `ALTER`
  operations that discard populated data unless the current phase explicitly
  approves them.
* Polymorphic references must have compensating integrity controls; do not rely
  on an unprotected `(entity_type, entity_id)` pair.
* Keep identity confidence and geometry/time confidence as separate concepts.

## Architecture Decision Discipline

* A decision that changes source trust, source priority, canonical field
  semantics, or entity-existence rules is not considered final until the
  corresponding decision is recorded in `docs/14-plan.md` in the same
  commit/PR as the code change.
* External API calls from deterministic pipelines such as skeleton sync,
  attachment, and verification must use the existing cache+quota path
  (`geocode_cache` / `api_quotas`).
* A new provider must implement the existing abstraction and reuse the existing
  integration path; do not create one-off external API call paths.
* Before changing the grouping or shape of data in a validated pipeline, verify
  and explicitly test that existing hard validators (for example monotonicity
  and speed checks) remain valid for the new representation.
* If the plan already specifies a missing mechanism, implement that mechanism
  instead of replacing it with a one-off manual operation.
* Avoid endless diagnostic/planning loops. Each investigation round must end
  with either:

  1. an implemented and validated change, or
  2. an explicit blocker that prevents safe implementation.

## Execution Discipline

For implementation tasks, prefer execution over extended deliberation.

1. Inspect only the code and documentation required to understand the task.
2. Make a reasonable implementation decision once the available evidence is
   sufficient.
3. Edit the code.
4. Inspect the diff.
5. Run focused validation.
6. Stop when the task is complete.

Do not:

* repeat equivalent searches or file reads;
* gather information only to increase confidence;
* reopen a decision that is already supported by the available evidence;
* create multiple implementation plans for a straightforward change;
* restart the analysis from the beginning after each tool result;
* refactor unrelated code.

If one missing fact blocks a safe implementation, obtain that fact with one
targeted inspection, then proceed.

Prefer the smallest correct change that satisfies the request.

## Testing

* `Makefile` targets are the preferred entry points for build and test commands.
* Go tests are the source of truth for API and behavioral correctness.
* Shell scripts under `scripts/` are manual demos, not correctness tests.
* Shared test scenarios belong in `test/common` and should be reusable by both
  integration and smoke tests.
* New MCP tools or fields must update the relevant shared scenarios and keep
  `scripts/api-demo.sh` current.
* Concurrency-sensitive mechanisms such as quotas and queues require explicit
  race-oriented tests.

## Observability

* Use structured logging (`slog` or an equivalent) with correlation to the
  relevant task/entity. The exact field schema belongs in the current plan.
* Long-running and background operations must accept `context.Context` and honor
  cancellation.

## Security

* Secrets must come from environment variables and must not be committed.
* Administrative operations such as imports, manual edits, and external API
  calls require authentication and auditability. The concrete mechanism belongs
  in the current plan.

## Code Style

* Do not add comments unless they are necessary for correctness or explicitly
  requested by the user.
* Code comments must be written in English. Existing Russian comments should
  be translated to English during code changes.
* Keep changes focused and consistent with existing code.
* Do not silently introduce architectural exceptions to solve local symptoms.

## Validation

After changing program code, run the narrowest relevant validation first.

For a normal Go code change, the expected final validation is:

```sh
gofmt
go vet ./...
make test
```

Run additional checks when relevant:

```sh
make check-layers
make check-deprecated
```

Do not claim validation was performed unless it was actually run.

## Stable Project Structure

```text
cmd/mcp-server/        entry point
internal/model/        canonical domain model
internal/mcp/          MCP tools
internal/server/       HTTP router and MCP mounting
internal/config/       configuration
internal/telemetry/    metrics and telemetry
test/common/           shared test scenarios and helpers
test/integration/      in-process HTTP+MCP tests
test/smoke/            tests using the real binary
testdata/              committed reference fixtures
tools/                 auxiliary Go modules
scripts/               manual demos and data-collection scripts
configs/               YAML configuration
migrations/             database DDL
docs/                  living project documentation
```

Packages related to storage, providers, adapters, verification, import,
GTFS compilation, and jobs evolve with the implementation plan. Their current
boundaries are defined by `docs/`, not by this file.

## Commands

```sh
make build
make test
make unit
make integration
make smoke
make vet
make fmt
make check-layers
make check-deprecated
make run
```

Current environment variables, provider flags, and demo parameters are defined
in `configs/` and the current project plan; do not duplicate them here.

## Documentation

`docs/` is the living documentation set. File names and numbering may change.

At the time this file was written, the main references are:

* `docs/02-glossary.md` — project terminology.
* `docs/13-mission.md` — project mission.
* `docs/14-plan.md` — current implementation plan, database structure, phases,
  synchronization model, and migration strategy.

Archived documents are not authoritative. Always prefer the current contents of
`docs/` over this list.
