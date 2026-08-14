# RawSyst POS — Project Overview

## Current state
As of 2026-08-14, this directory contains **only** the specification document
`RawSyst-POS-Blueprint-v2.4-FINAL.md` (v2.4, FINAL/FROZEN pre-System-Design).
No source code, package manifests, or build tooling exist yet. This memory
will need a follow-up pass once actual code scaffolding is added (fill in
suggested_commands.md, code style, test/lint/build commands at that point).

## Purpose
RawSyst POS is a multi-tenant, offline-first Retail ERP + POS platform for
Saudi Arabia and international clients — not just a POS, a full ERP (sales,
purchase, stock, warehouse, customer/supplier, accounting, HR/payroll,
online orders, delivery, CRM, analytics, legal/tax compliance, multi-store).

## Product / company
- Product: RawSyst POS, by RawSyst IT (rawsyst.com)
- Founder/Owner/Lead Developer: Mahedi Hasan Emon (mahedi.emon62@gmail.com)
- GitHub: https://github.com/mahedi-emon/RawSyst-POS

## Target tech stack (confirmed in blueprint, not yet implemented)
- Backend: Go (primary)
- Web frontend: Next.js + TypeScript
- Desktop POS: Tauri + React
- Cloud DB: PostgreSQL
- Offline/local DB: SQLite
- Cache/queue: Redis
- Infra: Docker, Nginx, GitHub Actions

## Non-negotiable architectural principles (from blueprint Part A2)
1. ERP, not just POS — every module talks to Accounting and Inventory automatically.
2. Compliance (ZATCA, VAT, PDPL) is core architecture, not bolted on later.
3. Offline-first is a hard requirement — POS sells with zero internet, syncs later without duplicates.
4. Nothing hard-coded to Saudi — country/tax/currency/language are configuration.
5. Every financial transaction is fully audit-trailed (who/what/when/where/before/after).
6. Permissions enforced server-side, never only hidden in UI.
7. Finalized invoices are immutable — corrections only via Credit/Debit Notes or Returns.

## Document structure (Parts A–O)
A: Platform Foundation (tenancy, RBAC, onboarding) · B: Core Retail Ops (product,
inventory, procurement, POS, sales, CRM) · C: Finance & Ops (double-entry
accounting, expenses, HR, payroll, shifts) · D: Intelligence & Communication
(reports, analytics, notifications, audit) · E: Saudi Legal/Tax/Payment
Compliance (ZATCA, VAT, PDPL, WPS) · F: Workflow Engine & External Portals ·
G: Internationalization · H: Platform Engineering & Security · I: Settings &
Customization · J: Technical Architecture & Stack · K: Module Tree · L/M:
Checklists · N: Regulatory Source Hierarchy · O: Requirements Traceability.
