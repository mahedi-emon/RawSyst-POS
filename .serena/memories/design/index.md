# System Design — complete index

All docs live in `docs/`. **Design is COMPLETE as of 2026-08-15.** Read the doc rather than re-deriving; read this index to know which doc to open.

## Foundation — `docs/system-design/`
| Doc | Settles |
|---|---|
| `00-architecture-overview` | Go modular monolith · deployment topology · RLS tenancy · event bus vs job queue · performance budget · plan ceilings · **RPO 15 min / RTO 4 h** (blueprint left these open) |
| `01-invoice-zatca-engine` | **PILLAR 1**. Invoice state machine · ICV/PIH allocation · **local signing on the terminal** · CSID key custody · B2B offline options A/B/C · Fatoora retry |
| `02-posting-engine` | **PILLAR 2**. Posting rules **as data** · the 12 rules · balance enforced by deferred DB constraint · fiscal periods · costing · the 9-effect atomic return |
| `03-sync-idempotency` | **PILLAR 3**. Terminal is authoritative for sales · 3-layer idempotency · per-entity conflict policy · **stock as movement deltas** · 500-invoice test satisfied by construction |
| `04-identity-tenancy-rbac` | Group→Company→Store→Terminal · RLS with FORCE · permissions resolved per request · 4 scope dimensions · field masking by omission |
| `05-regulatory-rule-registry` | Effective-dated legal values · GiST exclusion on overlaps · `verified_on` gate · Tier 1 sources only |
| `06-data-model` | Conventions · variant is the SKU · core ERD · indexes · local SQLite subset |
| `07-api-conventions` | REST shape · **money as decimal strings** · **404 not 403 cross-tenant** · idempotency header · cursor pagination |
| `08-background-jobs` | Postgres queue · sync vs async boundary · ZATCA infinite retry serialised per device · nightly integrity jobs |

## Phase 1 modules
| Doc | Covers |
|---|---|
| `10-catalog-and-inventory` | Product/variant split · barcode engine · movement deltas · costing incl. FIFO layers · tie-out invariant |
| `11-pos-and-sales` | Checkout path (no network) · VAT computation · multi-tender · returns (9 effects) · hardware · shift/X-Z |
| `12-accounting-vat-compliance` | Chart of accounts · cash/bank · VAT return + 4-way reconciliation · **legal hold vs PDPL erasure** · compliance screen |

## Later phases & compliance
| Doc | Covers |
|---|---|
| `20-later-phases` | Phases 2–5 boundaries, entities, integration points + 7 cross-phase constraints that never change |
| `90-regulatory-verification-checklist` | The 12 Part N claims + 3 E8.4 blockers. **For the founder and his advisors, not developers** |

## UI/UX — `docs/ui-ux/`
| Doc | Covers |
|---|---|
| `00-design-system` | Tokens · type scale · RTL mirroring · responsive · accessibility · component inventory |
| `01-screen-specs` | POS · dashboard · compliance · variant matrix · invoice · onboarding · shift close |
| `prototype.html` | Live interactive prototype, also published as an artifact |

## Related memories
[[architecture/decisions]] · [[architecture/phase-plan]] · [[blueprint/part-a-d-functional]] · [[blueprint/part-e-saudi-compliance]] · [[blueprint/part-f-k-architecture]] · [[code/backend-state]] · [[tooling/serena-setup]]
