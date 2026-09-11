# 00. Documentation Index

This is the only file that must be updated whenever the current plan or document
numbering changes. `AGENTS.md` and project documentation refer to the current
plan through this index rather than by hard-coded filename, so references remain
valid when the plan is revised.

## Current Documents

| File                    | Status           | Purpose                                                                                                                                            |
| ----------------------- | ---------------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| `docs/01-description.md`    | Current          | Project mission, principles, and scope                                                                                                             |
| `docs/14-plan.md`       | **Current plan** | Priorities (terminals first), database structure, phases, synchronization model, deprecation registry                                              |
| `docs/15-dev-status.md` | Current          | Current-phase snapshot: JSON → Postgres migration status, current package tree, environment variables, test-data references, deprecation checklist |
| `docs/12-motis-api.md`  | Current          | MOTIS API v6 and request examples                                                                                                                  |
| `docs/02-glossary.md`   | Current          | Terminology, `level` / `admin_level`, translation glossary                                                                                         |

## Archive

Archived documents are historical references only. Do not use them as the
source of truth for current implementation decisions.

| File                             | Reason                                                                         |
| -------------------------------- | ------------------------------------------------------------------------------ |
| `docs/01-description.md`         |                                           |
| `docs/03-architecture.md`        | Replaced by `14-plan.md`                                                       |
| `docs/05-roadmap.md`             | Replaced by phases in `14-plan.md`                                             |
| `docs/06-*.md`                   | Replaced by `14-plan.md`                                                       |
| `docs/phase0-validation.md`      | Historical MOTIS validation snapshot (2026-09-04); phase numbering is obsolete |

## Maintenance Rules

* When creating a new numbered document, add it here in the same commit.
* When the current plan is replaced by a new revision, update the **Current plan**
  entry here and move the previous plan to the archive.
* Never leave multiple documents marked as the current plan.
* `docs/15-dev-status.md` is a current-state snapshot. Rewrite it when a plan
  phase is completed; do not use it as a change history.
