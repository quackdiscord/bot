package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/redis/go-redis/v9"
)

// errUnavailable means Redis could not answer, so a rate-limit or
// idempotency decision could not be made. Callers fail closed with a 503.
var errUnavailable = errors.New("HTTP safety primitive unavailable")

// rateLimiter enforces fixed-window limits with one atomic Redis script.
type rateLimiter struct {
	client redis.UniversalClient
	prefix string
}

// rateDecision is the outcome of spending one request from a window.
type rateDecision struct {
	allowed    bool
	remaining  int
	retryAfter time.Duration
}

// rateLimitScript counts a request and starts the window on the first one,
// returning the count and the window's remaining milliseconds.
var rateLimitScript = redis.NewScript(`
local current = redis.call("INCR", KEYS[1])
if current == 1 then
  redis.call("PEXPIRE", KEYS[1], ARGV[1])
end
local ttl = redis.call("PTTL", KEYS[1])
return {current, ttl}
`)

func newRateLimiter(client redis.UniversalClient) *rateLimiter {
	return &rateLimiter{client: client, prefix: "http:rate:"}
}

// allow consumes one request from subject's current window.
func (l *rateLimiter) allow(ctx context.Context, subject string, limit config.Limit) (rateDecision, error) {
	windowMillis := max(limit.Window.Milliseconds(), 1)
	result, err := rateLimitScript.Run(ctx, l.client, []string{hashedKey(l.prefix, subject)}, windowMillis).Result()
	if err != nil {
		return rateDecision{}, fmt.Errorf("%w: rate limit: %w", errUnavailable, err)
	}
	values, ok := result.([]any)
	if !ok || len(values) != 2 {
		return rateDecision{}, fmt.Errorf("%w: invalid rate-limit response", errUnavailable)
	}
	current, err := redisInt64(values[0])
	if err != nil {
		return rateDecision{}, fmt.Errorf("%w: invalid rate-limit counter", errUnavailable)
	}
	ttlMillis, err := redisInt64(values[1])
	if err != nil {
		return rateDecision{}, fmt.Errorf("%w: invalid rate-limit TTL", errUnavailable)
	}
	if ttlMillis < 0 {
		ttlMillis = windowMillis
	}
	return rateDecision{
		allowed:    current <= int64(limit.Max),
		remaining:  max(limit.Max-int(current), 0),
		retryAfter: time.Duration(ttlMillis) * time.Millisecond,
	}, nil
}

// hashedKey keeps session credentials and user IDs out of Redis key names.
func hashedKey(prefix, material string) string {
	digest := sha256.Sum256([]byte(material))
	return prefix + hex.EncodeToString(digest[:])
}

func redisInt64(value any) (int64, error) {
	switch value := value.(type) {
	case int64:
		return value, nil
	case string:
		return strconv.ParseInt(value, 10, 64)
	case []byte:
		return strconv.ParseInt(string(value), 10, 64)
	default:
		return 0, fmt.Errorf("unexpected Redis integer type %T", value)
	}
}

// limit rate-limits a route class per subject and sets the RateLimit-*
// headers. Classes have separate counters, so one feature cannot starve
// another.
func (s *Server) limit(class string, limit config.Limit, subject func(*http.Request) string) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s.consume(w, r, class+":"+subject(r), limit) {
				next.ServeHTTP(w, r)
			}
		})
	}
}

// consume spends one request against key and reports whether the request
// may continue. If not, it has already written the response.
func (s *Server) consume(w http.ResponseWriter, r *http.Request, key string, limit config.Limit) bool {
	decision, err := s.limiter.allow(r.Context(), key, limit)
	if err != nil {
		writeError(w, r, http.StatusServiceUnavailable, codeDependency, "rate-limit service unavailable")
		return false
	}
	w.Header().Set("RateLimit-Limit", strconv.Itoa(limit.Max))
	w.Header().Set("RateLimit-Remaining", strconv.Itoa(decision.remaining))
	if !decision.allowed {
		retrySeconds := max(int(decision.retryAfter.Round(time.Second).Seconds()), 1)
		w.Header().Set("Retry-After", strconv.Itoa(retrySeconds))
		writeError(w, r, http.StatusTooManyRequests, codeRateLimited, "rate limit exceeded")
		return false
	}
	return true
}

// Endpoint classes for the policy that fronts every /guilds and /members/me
// route. The class picks the configured limit and names the counter.
const (
	classRead       = "member-read"
	classWrite      = "authenticated-write"
	classCaseCreate = "case-create"
	classRecovery   = "action-recovery"
)

// endpointClass is the default class for a method: reads and writes.
func endpointClass(method string) string {
	if isWrite(method) {
		return classWrite
	}
	return classRead
}

func (s *Server) classLimit(class string) config.Limit {
	switch class {
	case classWrite:
		return s.cfg.Limits.TemplateWrite
	case classCaseCreate:
		return s.cfg.Limits.CaseCreate
	case classRecovery:
		return s.cfg.Limits.Retry
	default:
		return s.cfg.Limits.MemberRead
	}
}

// policy is the outermost step of every guild and member route. It rejects
// malformed pagination before a handler can coerce it, then spends one
// request from the class's limit, keyed by credential and guild. Creating a
// case also spends from the evidence limit. It runs before authentication,
// so unauthenticated floods are limited too.
func (s *Server) policy(class string) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !validPagination(w, r) {
				return
			}
			subject := s.endpointSubject(r)
			if !s.consume(w, r, class+":"+subject, s.classLimit(class)) {
				return
			}
			if class == classCaseCreate && !s.consume(w, r, "evidence:"+subject, s.cfg.Limits.Evidence) {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// validPagination bounds limit, offset, and cursors on reads, writing a 400
// if they are out of range.
func validPagination(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet {
		return true
	}
	query := r.URL.Query()
	if query.Has("limit") {
		limit, err := strconv.Atoi(query.Get("limit"))
		if err != nil || limit < 1 || limit > 100 {
			writeError(w, r, http.StatusBadRequest, codeValidation, "limit must be between 1 and 100")
			return false
		}
	}
	if query.Has("offset") {
		offset, err := strconv.Atoi(query.Get("offset"))
		if err != nil || offset < 0 || offset > 100000 {
			writeError(w, r, http.StatusBadRequest, codeValidation, "offset must be between 0 and 100000")
			return false
		}
	}
	for _, name := range []string{"before_id", "cursor"} {
		if query.Has(name) && len(query.Get(name)) > 256 {
			writeError(w, r, http.StatusBadRequest, codeValidation, name+" is too long")
			return false
		}
	}
	return true
}

// endpointSubject keys the endpoint policy by session credential and guild.
// The credential is only ever stored hashed.
func (s *Server) endpointSubject(r *http.Request) string {
	return s.sessionID(r) + ":" + r.PathValue("discordGuildID") + ":" + r.PathValue("guildID")
}

// endpointWriteSubject scopes a core write's idempotency key to the caller,
// guild, and exact request target.
func (s *Server) endpointWriteSubject(r *http.Request) string {
	return s.endpointSubject(r) + ":" + r.Method + ":" + r.URL.EscapedPath()
}

// clientIPSubject keys the public OAuth limits by client address.
func (s *Server) clientIPSubject(r *http.Request) string {
	return "ip:" + s.clientIP(r)
}

// memberSubject keys member routes by the signed-in Discord user.
func memberSubject(r *http.Request) string {
	return sessionFrom(r.Context()).DiscordUserID
}

// guildActorSubject keys appeal-staff and module routes by guild and actor.
func guildActorSubject(r *http.Request) string {
	staff := quack.StaffFromContext(r.Context())
	return staff.Guild.ID + ":" + staff.ActorDiscordUserID
}

// moduleWriteSubject scopes a module write's idempotency key to the actor
// and exact request target.
func moduleWriteSubject(r *http.Request) string {
	return guildActorSubject(r) + ":" + r.Method + ":" + r.URL.EscapedPath()
}

// memberWriteSubject scopes a member write's idempotency key to the member,
// route, and the case or appeal it names.
func memberWriteSubject(r *http.Request) string {
	return memberSubject(r) + ":" + routeFrom(r.Context()) + ":" + r.PathValue("caseID") + ":" + r.PathValue("appealID")
}

// staffAppealWriteSubject scopes an appeal review write's idempotency key to
// the reviewer, route, and appeal.
func staffAppealWriteSubject(r *http.Request) string {
	return guildActorSubject(r) + ":" + routeFrom(r.Context()) + ":" + r.PathValue("appealID")
}

// parseTrustedProxies accepts bare IPs and CIDRs, as config.Validate does.
func parseTrustedProxies(proxies []string) ([]*net.IPNet, error) {
	nets := make([]*net.IPNet, 0, len(proxies))
	for _, proxy := range proxies {
		if ip := net.ParseIP(proxy); ip != nil {
			bits := 8 * net.IPv6len
			if ip4 := ip.To4(); ip4 != nil {
				ip, bits = ip4, 8*net.IPv4len
			}
			nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, cidr, err := net.ParseCIDR(proxy)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy %q", proxy)
		}
		nets = append(nets, cidr)
	}
	return nets, nil
}

// clientIP returns the caller's address for per-IP rate limits. Forwarding
// headers are believed only when the direct peer is a trusted proxy;
// X-Forwarded-For is read right to left and the first untrusted hop wins.
func (s *Server) clientIP(r *http.Request) string {
	remote, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		return ""
	}
	remoteIP := net.ParseIP(remote)
	if remoteIP == nil {
		return ""
	}
	if !s.trustedProxy(remoteIP) {
		return remote
	}
	for _, header := range []string{"X-Forwarded-For", "X-Real-IP"} {
		hops := strings.Split(r.Header.Get(header), ",")
		for i := len(hops) - 1; i >= 0; i-- {
			hop := strings.TrimSpace(hops[i])
			ip := net.ParseIP(hop)
			if ip == nil {
				break
			}
			if i == 0 || !s.trustedProxy(ip) {
				return hop
			}
		}
	}
	return remote
}

// trustedProxy reports whether ip is in api.trusted_proxies.
func (s *Server) trustedProxy(ip net.IP) bool {
	return slices.ContainsFunc(s.trustedProxies, func(n *net.IPNet) bool { return n.Contains(ip) })
}
