# Specification Version

**Version: 2.1.0**
**Status: FROZEN** (updated 2026-07-02)

The ClaudWorker V2 architecture specification (`docs/00`–`docs/22` + `docs/ARCHITECTURE_REVIEW.md`) is
frozen at **v2.1.0**. Implementation targets **exactly** this version
([21_ImplementationRoadmap](docs/21_ImplementationRoadmap.md)).

The only change from 2.0.0 is the build **sequence** ([ACP-0001](docs/acp/ACP-0001-assignment-engine-before-database.md)):
the Assignment Engine is built before the full database (persistence emerges from real need). No
component, law, schema contract, workflow, or invariant changed — architecture design is identical.

## Change policy

No architectural change is made except through an approved **Architecture Change Proposal (ACP)** —
see [ACP_TEMPLATE.md](ACP_TEMPLATE.md) and the Implementation Discipline section of
[21_ImplementationRoadmap](docs/21_ImplementationRoadmap.md).

An approved ACP bumps this version using semantic versioning and updates every affected document in
the same change:

- **MAJOR** — a breaking change to a documented contract, law, schema, workflow, or invariant.
- **MINOR** — an additive capability that doesn't break existing contracts.
- **PATCH** — a clarification/typo/consistency fix with no behavioral change.

## History

| Version | Date | Change |
|---|---|---|
| 2.1.0 | 2026-07-02 | [ACP-0001](docs/acp/ACP-0001-assignment-engine-before-database.md): re-sequence roadmap — Assignment Engine (walking skeleton, ports + minimal adapters) built before the full database; persistence emerges (new S3). Build order only; no design change. |
| 2.0.0 | 2026-07-01 | Initial frozen architecture. Automation field, configurable thresholds, Plugin naming normalized, Lock Manager V1 (issue/device/merge), Knowledge Brain + Execution State split, Assignment terminology, restart safety (Law 19), Decision Engine, Migration phase, Implementation Roadmap. |
