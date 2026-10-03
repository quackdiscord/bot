# HTTP API Platform Contract

The HTTP API supports Quack's dashboard and internal adapters. It is not a
public automation API in v5. `httpapi.PlatformRegistrar` installs the shared
security contract before feature routes; the integration checkpoint must call
it before registering routes.

## Authentication and browser security

Discord OAuth state is single-use and expires in Redis. A successful callback
stores Discord tokens only inside the server-side Redis session. Public callback
and `/auth/me` JSON never contain a session ID, access token, or refresh token.
They intentionally return the non-secret CSRF challenge so a separately hosted,
allowed dashboard origin can populate the required request header.

Quack uses a stable forced-reauthentication flow rather than refreshing Discord
OAuth grants. When either the session or Discord token expires, middleware
revokes the Redis session, clears both cookies, and returns
`reauthentication_required`. Revoked or rejected Discord grants use the same safe
client behavior instead of returning Discord details. `POST /auth/logout`
revokes the current session. `POST /auth/logout-all` and
`Store.RevokeUserSessions` revoke every indexed session for compromise response
or account-change integration.

Production session cookies are `Secure`, `HttpOnly`, path `/`, and
`SameSite=Lax`. The separate host-only CSRF cookie is `Secure`, non-HttpOnly,
path `/`, and `SameSite=Lax`; the authenticated callback or `/auth/me` response
provides its matching challenge to allowed dashboard JavaScript even when the
dashboard and API use different hosts. Cookie-authenticated `POST`, `PUT`, `PATCH`, and
`DELETE` requests require an allowed exact `Origin` plus an `X-CSRF-Token`
header matching the CSRF cookie. Explicit bearer-authenticated internal adapter
requests do not use browser CSRF credentials.

## HTTP boundaries and errors

Outside development, an explicit non-wildcard CORS allowlist and secure cookies
are startup requirements. Request bodies default to 1 MiB. Read-header, read,
write, and idle phases default to 5, 15, 30, and 60 seconds. Responses include a
deny-by-default content security policy, no-sniff, frame denial, no-referrer,
permissions policy, and no-store headers.

Forwarded client IP headers are ignored by default, so they cannot be rotated to
bypass OAuth limits. Deployments behind a proxy must list only that proxy's IP
or CIDR in `api.trusted_proxies`; malformed entries fail startup.

Every failure response has this shape:

```json
{
  "error": {
    "code": "validation_failed",
    "message": "request validation failed",
    "request_id": "...",
    "correlation_id": "..."
  }
}
```

Authentication, authorization, validation, not-found, conflict, rate-limit,
oversized-body, dependency, and internal errors use stable codes and safe
messages. Legacy route errors are normalized at the platform boundary, so raw
Discord/service errors do not escape. Request logs record only the URL path,
status, latency, and trace IDs; they do not record query strings, authorization
headers, cookies, session IDs, OAuth codes, or tokens.

## Rate-limit and idempotency primitives

`apps/backend/internal/httpapi/platform` exposes fail-closed Redis primitives. Actor, guild,
and caller keys are SHA-256 hashed before Redis storage. Redis unavailability
returns `dependency_unavailable`; it never silently bypasses a limit or lease.

| Class | Default | Integration scope |
| --- | --- | --- |
| OAuth | 20 per 10 minutes | Client IP; installed by QP-B |
| Member reads | 120 per minute | Authenticated Discord user |
| Template writes | 30 per minute | Actor plus guild |
| Case creation | 20 per minute | Actor plus guild |
| Retry controls | 10 per minute | Actor plus guild |
| Evidence capture | 20 per minute | Actor plus guild |

The fixed-window limiter returns remaining capacity and a deterministic retry
interval. The idempotency store returns `acquired`, `in_progress`, or the
original `complete` status/body. Lease tokens fence stale workers; TTL expiry is
the only automatic release, and completed results default to 24-hour retention.
Feature registrars in QP-D/QP-H own applying these primitives to their routes;
QP-B exposes `platform.FromRepository` without editing the integration-owned
router or runtime files.
