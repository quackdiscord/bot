# Quack v5 dashboard API policy

The HTTP API is the dashboard and internal-adapter boundary, not a public
third-party automation API. Authentication is Discord session/bearer based;
guild routes refresh live Discord membership and capability before protected
work. Former staff lose access on the next sensitive request. Member-owned
routes remain target-scoped and cannot enumerate another member.

## Route policy matrix

| Route class | Rate policy | Write replay policy | Capability |
| --- | --- | --- | --- |
| OAuth login/callback | `limits.oauth`, client IP | OAuth state is single-use | unauthenticated |
| Member-owned reads/appeals | `limits.member_read`, session | `Idempotency-Key` for appeal writes | case target only |
| Template/settings/module writes | `limits.template_write`, actor and guild | required, Redis fenced | Manage Guild/Administrator/owner |
| Case creation | `limits.case_create`, actor and guild | required plus durable case key | Moderate Members plus selected action permission/hierarchy |
| Retry/reversal controls | `limits.retry`, actor and guild | required plus action fencing | Moderate Members plus original action permission/hierarchy |
| Evidence capture | `limits.evidence`, actor and guild | required | case capability and live message access |
| Staff/admin/member reads | `limits.member_read`, actor and guild | not applicable | route-specific live capability/ownership |

Redis unavailability returns a stable `503` and does not execute protected
work. Concurrent/restarted replay returns the original response or an
in-progress conflict. Keys are scoped by actor, guild, method, and route and are
hashed before Redis storage.

All browser mutations, including `PUT`, require exact configured CORS origin
and CSRF double-submit validation. Preflight advertises `GET, POST, PUT, PATCH,
DELETE, OPTIONS`. Request bodies and server phases are bounded. Errors contain
only stable code/message/request/correlation identifiers. Logs and persisted
audit metadata recursively redact OAuth tokens, sessions, cookies, webhook
URLs, member content, and request/response/action payloads.
