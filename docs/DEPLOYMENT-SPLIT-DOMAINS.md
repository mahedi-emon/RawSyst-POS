# Giving the control plane its own domain

How to put the business application and the platform control plane on two
hostnames, with Nginx Proxy Manager, Cloudflare and Portainer.

```
https://app.example.com       the business application
https://console.example.com   the platform control plane
```

Both are served by the same containers. There is no second build, no second
stack and no second database. One environment variable decides which hostname
serves which half.

Nothing in this document has been applied to any server. It is the procedure.

## What the split actually gives you

**Session separation, for free.** The refresh cookie is set with no `Domain`
attribute, which makes it host-only: the browser returns it to exactly the host
that set it and to no sibling subdomain. An operator signing in at the console
holds a session structurally unable to reach the business hostname, and a shop's
session cannot reach the console. No CORS is introduced and no cookie attribute
is weakened.

**Reduced surface.** The platform API stops answering at the address every shop
has in their browser history. A credential stolen from a shop cannot even be
tried against the control plane there, and scanning the business hostname
reveals no control plane to attack.

**What it is not.** It is not the security boundary. Every platform route
answers 404 to a caller who is not a platform operator, and that is what
protects the control plane. A `Host` header can say anything, so the split
refuses nothing that authorization would have allowed. It is a second lock on a
door that is already locked, and worth having on those terms.

## Order of operations

Follow this order. The application is designed so that each step is safe on its
own and nothing breaks between them.

1. **Add the DNS record.** An A or AAAA record for the console name, pointing at
   the same server as the business name. If Cloudflare is proxying the business
   name, proxy this one too.
2. **Add the proxy host in Nginx Proxy Manager** and issue its certificate. At
   this point the console name resolves and serves the business application,
   because the split is still off. That is expected and harmless.
3. **Set the environment variables** in the Portainer stack and redeploy.
4. **Check both hostnames.** The section at the end lists exactly what to check.

Do not set the variables before the certificate exists. The console would be
configured and unreachable, and `/platform` would have stopped answering on the
business hostname — locking you out of your own control plane until DNS
propagates.

## Nginx Proxy Manager

Two proxy hosts, both pointing at the same container and port.

### The business host

| Field | Value |
|---|---|
| Domain Names | `app.example.com` |
| Scheme | `http` |
| Forward Hostname / IP | the web container, e.g. `rawsyst_web` |
| Forward Port | `3000` |
| Cache Assets | off |
| Block Common Exploits | on |
| Websockets Support | **on** |

### The console host

| Field | Value |
|---|---|
| Domain Names | `console.example.com` |
| Scheme | `http` |
| Forward Hostname / IP | the same container |
| Forward Port | the same port |
| Websockets Support | **on** |

On the **SSL** tab of each: request a certificate, then turn on **Force SSL**
and **HTTP/2 Support**. Leave **HSTS** off until you have confirmed both
hostnames work over HTTPS; turning it on before that makes a mistake expensive
to undo in a browser that has cached it.

**Scheme is `http`, not `https`.** Nginx Proxy Manager terminates TLS and talks
to the container in plain HTTP over the Docker network. Setting `https` here
makes it try to speak TLS to a container that is not serving it, which fails in
a way that looks like the application being down.

**Websockets on.** The product opens a socket for live updates. Without this the
screens still load and the live figures stop arriving, which reads as a stale
dashboard rather than as a proxy setting.

**Host header.** Nginx Proxy Manager forwards the original `Host` by default and
nothing needs adding. The split is decided from that header, so if you have a
custom Advanced configuration that overrides it, remove the override.

If you use the bundled `deploy/nginx/nginx.conf` instead of Nginx Proxy Manager,
it contains a prepared console server block, commented out. That file is an
**optional alternative** and is not needed for this deployment.

## Cloudflare

**DNS.** One record per hostname, both pointing at the server. Proxy status is
your choice; if the business name is proxied, proxy the console too, so both
have the same path to the origin and behave the same way.

**SSL/TLS mode: Full (strict).** Cloudflare holds a certificate for the browser,
and Nginx Proxy Manager holds one for Cloudflare. Both certificates must be
valid for both hostnames.

Do **not** use Flexible. Flexible means Cloudflare speaks plain HTTP to an
origin that is redirecting HTTP to HTTPS, which is the classic redirect loop:
the browser sees an endless chain and the site appears broken. Force SSL in
Nginx Proxy Manager plus Flexible in Cloudflare is exactly that loop.

**Always Use HTTPS: on.** **Automatic HTTPS Rewrites: on**, which prevents
mixed-content warnings from any absolute URL that slipped through as `http`.

**Origin certificates.** If you would rather not run Let's Encrypt on the
server, issue a Cloudflare Origin Certificate covering both names and install it
in Nginx Proxy Manager as a custom certificate. It is only trusted by
Cloudflare, so it works with Full (strict) and not with a browser hitting the
origin directly.

**Cloudflare preserves the `Host` header** to the origin, so the split works
through it with no extra configuration.

## Portainer

Add these to the stack's environment. Everything else stays as it is.

```env
RAWSYST_CONSOLE_HOST=console.example.com
RAWSYST_CONSOLE_URL=https://console.example.com
RAWSYST_APP_URL=https://app.example.com
```

`RAWSYST_CONSOLE_HOST` is a **hostname**: no scheme, no path, no trailing slash.
A pasted URL is reduced rather than refused, but the plain hostname is what it
means.

`RAWSYST_CONSOLE_URL` is the **full address**. It is kept separate rather than
derived, because behind a proxy that terminates TLS the scheme is not knowable
from inside the container. It is used in exactly one place: telling a platform
operator who signed in on the business hostname where to go instead. It is
answered only to an authenticated operator and never compiled into the browser
bundle, so setting it does not publish the console's address to every shop.

`RAWSYST_APP_URL` is the **business** address. It shipped under this name before
the split existed; there is no separate `BUSINESS_APP_URL`, and renaming it
would break every deployment that already sets it.

Compose passes the console hostname to both the API and the web container,
because they enforce different halves of the same rule: the web tier decides
which pages a hostname serves, and the API decides which routes it serves.

Redeploy the stack after changing these. They are read when a process starts,
so a restart is required and a rebuild is not.

**No secrets are involved.** None of these three values is sensitive, and none
belongs in a secrets store.

## What the split changes

### On the business hostname

| Path | Before | After |
|---|---|---|
| `/` and every business screen | 200 | 200 |
| `/platform` and below | 200 for an operator | **404** |
| `/api/v1/platform/*` | 200 for an operator | **404** |
| `/api/v1/*` business routes | 200 | 200 |
| `/login`, password screens | 200 | 200 |
| `/api/v1/auth/*` | 200 | 200 |
| `/healthz`, `/readyz` | 200 | 200 |

### On the console hostname

| Path | After |
|---|---|
| `/` | 200, and sends an operator straight to `/platform` |
| `/platform` and below | 200 |
| every other page | **404** |
| `/api/v1/platform/*` | 200 |
| `/api/v1/*` business routes | **404** |
| `/login`, password screens | 200 |
| `/api/v1/auth/*`, `/api/v1/meta/*`, `/api/v1/plans` | 200 |
| `/healthz`, `/readyz` | 200 |

Sign-in, the password screens, the second factor and the version endpoint answer
on both, because the console signs in through the same endpoint as everybody
else and must reach it on **its own origin** — otherwise the `SameSite=Strict`
refresh cookie is never sent, and the only fixes would be CORS and a weakened
cookie. That is the trade this design exists to avoid.

Health checks answer to **any** hostname, including the container's own name and
IP. Docker's health check addresses the container as `localhost`, and refusing
it would restart a healthy service in a loop.

A hostname that is neither is treated as the business hostname. That fails
towards reachability rather than refusal, so a proxy passing something nobody
predicted does not lock everybody out — and the direction that matters is still
enforced, because the platform API answers only on the console.

### `/platform` on the business hostname

It returns 404, with no redirect. A redirect would publish the console's address
to whoever guessed the path, which undoes most of the value of not linking to it
anywhere.

An operator who signs in on the business hostname is **not** left stranded. The
sign-in completes, and the landing page sends them to the console using the
address from their own `/auth/me` response — which is answered only to a
platform operator.

**No loop.** The console serves that same landing page at its own root, because
an operator types `console.example.com` and not `console.example.com/platform`.
Arriving there, the page compares its own origin against the configured console
address; they match, so it navigates locally to `/platform` instead of jumping
to the address it is already on. Getting that comparison wrong is an endless
redirect on the one screen an operator always starts from, so it is asserted in
`web-next/src/lib/origin.test.ts` rather than left to review.

## Local development

Nothing changes. With the variables unset, both halves share one origin exactly
as before, `/platform` works on `localhost`, and every test that does not
explicitly configure a console host sees the old behaviour.

To exercise the split locally, set `RAWSYST_CONSOLE_HOST=console.localhost` and
use that name in the browser. Modern browsers resolve `*.localhost` to loopback
without a hosts-file entry.

## Verifying it

After redeploying, from any machine:

```bash
# The control plane is absent from the business hostname.
curl -s -o /dev/null -w '%{http_code}\n' https://app.example.com/platform
curl -s -o /dev/null -w '%{http_code}\n' https://app.example.com/api/v1/platform/health
# both: 404

# And present on the console.
curl -s -o /dev/null -w '%{http_code}\n' https://console.example.com/platform
# 200

# The business application is absent from the console.
curl -s -o /dev/null -w '%{http_code}\n' https://console.example.com/dashboard
# 404

# Signing in works on both, which is what keeps the strict cookie working.
curl -s -o /dev/null -w '%{http_code}\n' https://app.example.com/login
curl -s -o /dev/null -w '%{http_code}\n' https://console.example.com/login
# both: 200

# Health answers everywhere.
curl -s -o /dev/null -w '%{http_code}\n' https://console.example.com/healthz
# 200
```

Then sign in as a platform operator at the console and confirm the panel loads,
and sign in as a shop at the business hostname and confirm the till does.

If the console shows the business application instead of the control plane, the
`Host` header is not reaching the container: check for an Advanced override in
Nginx Proxy Manager that sets it to something else.

## Rolling back

Clear `RAWSYST_CONSOLE_HOST` and redeploy. Both halves return to one origin
immediately. The DNS record and the certificate can stay; the console hostname
simply serves the business application again, which is what it did between steps
2 and 3 of the procedure above.

## Limitations

**The JavaScript bundle is not separated.** Both hostnames are served by one
build, so the control plane's interface chunks exist in the deployment either
way. They contain interface code and no data, and every byte they could fetch is
behind the API's 404. Separating them would mean a second build and a shared-
component extraction; `docs/SUPERADMIN-SEPARATION-PLAN.md` sets out that trade.

**No IP allow-list.** Deliberately not added. It is the strongest cheap control
available for a control plane and also the one that locks an operator out of
their own platform from a hotel, so it is a decision rather than a default.

**The `Host` header is not proof.** A caller can claim any hostname. The split
reduces surface; `RequireSuperAdmin` is what refuses people, and
`docs/ACCESS-BOUNDARIES.md` records how that is verified.
