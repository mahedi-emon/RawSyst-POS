# Regulatory source files

This directory is mounted read-only into the `regulatory` container as
`/sources`. Put the filled-in source file here.

A source file is one person's written statement of what the official documents
say, and the figures they read. RawSyst supplies everything else — which
document, which articles, what each field means, what unit it is in and what
shape the answer takes. It does not supply the figures, and deliberately does
not: there is no machine-readable authoritative source for them, so a number
shipped in this product would be a number somebody typed from memory, presented
to every deployment as though the software had established it.

    docker compose --profile setup run --rm regulatory
    docker compose --profile setup run --rm regulatory -template -country sa > deploy/regulatory/sources.json
    # somebody reads the articles that file cites and fills in the figures
    docker compose --profile setup run --rm regulatory -check -file /sources/sources.json
    docker compose --profile setup run --rm regulatory -apply -file /sources/sources.json

With no arguments it reports what this installation is still waiting for and
exits non-zero while anything is blocking release, which is what the API's own
start-up gate is checking.

Filled-in files are not committed: they carry a named person's attestation and
they are specific to what a deployment has recorded. `.gitignore` in this
directory keeps them out.
