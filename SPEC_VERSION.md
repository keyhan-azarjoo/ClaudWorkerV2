# Specification Version

**Version: 2.0.0**
**Status: FROZEN** (2026-07-01)

The ClaudWorker V2 architecture specification (`docs/00`–`docs/22` + `docs/ARCHITECTURE_REVIEW.md`) is
frozen at **v2.0.0**. Implementation targets **exactly** this version
([21_ImplementationRoadmap](docs/21_ImplementationRoadmap.md)).

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
| 2.0.0 | 2026-07-01 | Initial frozen architecture. Automation field, configurable thresholds, Plugin naming normalized, Lock Manager V1 (issue/device/merge), Knowledge Brain + Execution State split, Assignment terminology, restart safety (Law 19), Decision Engine, Migration phase, Implementation Roadmap. |
