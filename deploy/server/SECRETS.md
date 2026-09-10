# Secrets and server configuration

**A database dump contains no secrets and no server configuration.** That is
deliberate, it is the single most important sentence in this document, and it
has a consequence: *a verified backup is not enough to rebuild this server.*
Something has to carry the values below, and that something is not the backup.

This is the checklist for the something.

Its companions: **[BACKUP.md](BACKUP.md)** is the database,
**[RECOVERY.md](RECOVERY.md)** is what to do when something has gone wrong, and
**[MIGRATION.md](MIGRATION.md)** is moving to another server on purpose.

---

## Why they are not in the backup

A backup is read by whoever can list the bucket. A backup that carried the
credentials to the system it backs up would turn one leaked object into a full
compromise: restoring a stolen dump is bad, and being able to sign valid
sessions against the running system afterwards is worse.

So the dump carries business data and nothing else. Values the *application*
seals — the ZATCA credential, payment gateway keys, MFA secrets — are in the
dump, but as ciphertext, and they are unreadable without a key that is not.

---

## The inventory

Everything here lives in `.env` on the server, which is `chmod 600`, is not in
git, and must never go in.

### Lose these and the data is gone

| | What it is | If it is lost |
|---|---|---|
| `RAWSYST_DATA_ENCRYPTION_KEYS` | Seals the ZATCA credential, gateway keys and MFA secrets *inside* the database | The database still restores. Those columns are ciphertext for ever. |
| `RAWSYST_BACKUP_ENCRYPTION_KEY` | AES-256-GCM over the dump itself | **Every encrypted backup is unreadable by anybody, permanently, including this product.** |

These two are the reason this document exists. Nothing on the server can
recover either, and that is the arrangement rather than a gap.

### Lose these and something breaks until it is replaced

| | What it is | If it is lost |
|---|---|---|
| `POSTGRES_PASSWORD` | The application's database role | Resettable by a superuser; the database is fine |
| `RAWSYST_BACKUP_ROLE_PASSWORD` | The backup role's password | Re-run `backup role` with a new one |
| `RAWSYST_JWT_SECRET` | Signs sessions | A new one signs everybody out. See the note below |
| `RAWSYST_S3_ACCESS_KEY_ID` / `_SECRET_ACCESS_KEY` | Reaches the object store | Issue new credentials at the provider |
| `RAWSYST_METRICS_TOKEN` | Guards `/metrics` | Generate a new one |
| `RAWSYST_SENTRY_DSN` | Error reporting | Optional; take a new DSN |
| `RAWSYST_REDIS_PASSWORD` | Only with the `scale` profile | Reset it |

### Configuration, not secret, but still not in the dump

`RAWSYST_ENV`, `RAWSYST_DATA_REGION`, `RAWSYST_CORS_ORIGINS`,
`RAWSYST_PLATFORM_EMAIL`, `RAWSYST_DB_MAX_CONNS`, `TZ`, the port numbers, the
retention counts, `RAWSYST_ALLOW_PRODUCTION_RESTORE`, and everything else in
`.env.example`. Rebuildable from `.env.example` plus what this deployment
decided, but only if somebody wrote down what it decided.

Outside `.env` and equally absent from a dump: the TLS certificate and key
(re-issued by ACME, so not worth carrying), the Caddyfile or nginx config, the
systemd units in this directory, the firewall rules, and the SSH keys that get
you onto the box. All of those are in this repository or re-creatable from
`RUNBOOK.md` — except the SSH keys, which are yours.

---

## Where to keep them

One rule: **not only on this server, and not in the backup bucket.** A key kept
beside the thing it encrypts is decoration.

In rough order of preference:

1. The organisation's existing secret store, if there is one.
2. A password manager, as a single secure note holding the whole `.env`.
3. Printed, sealed, and in a different building. Unfashionable and it has
   recovered more systems than option 1.

For the two keys in the first table, use **two** of these. They have no
recovery path.

---

## Setting up a new server

In this order. The database restore is step 5, not step 1, and the order is the
point.

- [ ] 1. Build the machine as far as the first `docker compose up` in
      [RUNBOOK.md](RUNBOOK.md), then stop.
- [ ] 2. Write `.env` from the secret store. Not from `.env.example` and not
      from memory: the values have to be the same ones.
- [ ] 3. `chmod 600 .env`, and confirm `git status` does not list it.
- [ ] 4. Bring up **the database only**, and nothing else.
- [ ] 5. Create the backup role, before anything needs it:
      `docker compose ... run --rm backup role`
- [ ] 6. Restore, following [RECOVERY.md](RECOVERY.md).
- [ ] 7. Prove the backup key still works on this machine:
      `docker compose ... run --rm backup verify`
- [ ] 8. Bring the rest up.
- [ ] 9. Take a backup **on the new server** and verify it, before anybody
      trades on it.

---

## The two decisions somebody has to make

**`RAWSYST_JWT_SECRET`, at migration time.** Restoring onto a new server with a
*different* secret is safe and signs everybody out. With the *same* secret,
existing sessions keep working across the move. The first is tidier, the second
is kinder, and either is defensible — but it has to be a decision rather than an
accident, because "why is everyone logged out" at 09:00 on a Monday is an
expensive way to discover it.

**`RAWSYST_BACKUP_ENCRYPTION_KEY`, before the first backup.** Turning encryption
on later leaves the earlier snapshots readable by whoever can list the bucket;
turning it off later leaves the earlier ones needing a key you must still keep.
Decide once, at the start, and write the key down twice. BACKUP.md sets out what
turning it on commits you to.

---

## Rotating one

Nothing here rotates automatically, on purpose: a rotation that happens without
a person is a rotation nobody notices has half-failed.

| | How |
|---|---|
| `POSTGRES_PASSWORD` | `ALTER ROLE ... PASSWORD`, update `.env`, restart |
| Backup role password | `RAWSYST_BACKUP_ROLE_PASSWORD=... backup role`, update `RAWSYST_BACKUP_DSN`, restart |
| `RAWSYST_JWT_SECRET` | Replace and restart. Everybody signs in again |
| `RAWSYST_METRICS_TOKEN` | Replace, restart, update the scraper |
| Object store credentials | Issue new ones at the provider, replace, restart |
| `RAWSYST_BACKUP_ENCRYPTION_KEY` | **Keep the old one.** A new key applies to new backups only; every existing snapshot still needs the key it was sealed with, and the manifest fingerprint says which |
| `RAWSYST_DATA_ENCRYPTION_KEYS` | Supports more than one value so old ciphertext stays readable while new writes use the new key. Never remove a key that sealed something still in the database |

One caveat that belongs here rather than in a comment: `backup role` sends
`CREATE ROLE ... PASSWORD` to Postgres, and a server configured with
`log_statement = all` writes statements to its own log. Do not run a production
database that way. If one did, treat the backup role password as exposed and
rotate it.

---

## What this checklist cannot do

It cannot prove somebody has actually kept these values. The only thing that
proves it is doing the drill in [RECOVERY.md](RECOVERY.md) on a spare machine,
from the secret store rather than from this server, with a stopwatch.

Until that has been done once, "we have backups" is a statement about the
database and not about the system.
