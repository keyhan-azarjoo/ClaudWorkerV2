# Architecture Change Proposal (ACP) — Template

Copy this file to `docs/acp/ACP-NNNN-short-title.md` and fill it in. **No architecture change may be
implemented until its ACP is approved by the owner.** See the Implementation Discipline section of
[21_ImplementationRoadmap](docs/21_ImplementationRoadmap.md) and [SPEC_VERSION.md](SPEC_VERSION.md).

---

- **ACP:** NNNN
- **Title:**
- **Author:** keyhanazarjoo
- **Date:**
- **Status:** Draft | Proposed | Approved | Rejected | Superseded
- **Discovered during:** (subsystem Sn / operation)
- **Target spec version:** (e.g. 2.1.0 — MAJOR/MINOR/PATCH and why)

## 1. Problem / gap
What missing or contradictory architectural requirement was discovered? Why can't implementation
proceed under the current frozen spec? (Be specific — cite the doc/law/schema/invariant.)

## 2. Evidence
Concrete facts that prove the gap (failing case, contradiction between two docs, an impossible
requirement). No speculation.

## 3. Proposed change
The exact change to the architecture. Precise enough to implement without further interpretation.

## 4. Affected documents / laws / schemas
List every doc (`docs/NN_*.md`), System Law, DB schema, workflow, invariant, or config key that must
change, and how.

## 5. Alternatives considered
At least one alternative, and why it was not chosen. Include the **simpler** option and why it does or
doesn't suffice (Law 17).

## 6. Determinism & token impact
Does this keep the change deterministic (Go) where the spec requires it (Laws 5/6/18)? Any token-cost
impact? (Prefer deterministic; justify any new AI use.)

## 7. Compatibility & migration
Effect on existing projects/config/Brain/Execution State. Is it backward compatible? Migration steps
if not.

## 8. Rollback
How to revert if the change proves wrong.

## 9. Decision
- **Owner decision:** Approved / Rejected / Needs changes —
- **New spec version:**
- **Follow-up:** docs updated in the same change? (yes/no)
