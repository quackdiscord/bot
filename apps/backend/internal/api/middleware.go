package api

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
)

// middleware wraps a handler with one step of the request pipeline.
type middleware func(http.Handler) http.Handler

// chain wraps h so that mws run in the order given.
func chain(h http.Handler, mws ...middleware) http.Handler {
	for _, mw := range slices.Backward(mws) {
		h = mw(h)
	}
	return h
}

type contextKey int

const (
	routeKey contextKey = iota
	sessionKey
	guildStaffKey
)

func withRoute(ctx context.Context, pattern string) context.Context {
	return context.WithValue(ctx, routeKey, pattern)
}

// routeFrom returns the matched ServeMux pattern, such as
// "GET /guilds/{discordGuildID}/cases", or "" if no route matched.
func routeFrom(ctx context.Context) string {
	pattern, _ := ctx.Value(routeKey).(string)
	return pattern
}

const (
	requestIDHeader     = "X-Request-ID"
	correlationIDHeader = "X-Correlation-ID"
)

// requestContext adopts or generates the request and correlation IDs and
// echoes them back, so a dashboard error can be matched to server logs.
func requestContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := quack.ContextWithTrace(r.Context(), r.Header.Get(requestIDHeader), r.Header.Get(correlationIDHeader))
		requestID, correlationID := quack.TraceIDsFromContext(ctx)
		w.Header().Set(requestIDHeader, requestID)
		w.Header().Set(correlationIDHeader, correlationID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// captureWriter buffers a response so it can be inspected or rewritten
// before anything reaches the client. Headers pass straight through.
type captureWriter struct {
	w      http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (c *captureWriter) Header() http.Header { return c.w.Header() }

func (c *captureWriter) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
	}
}

func (c *captureWriter) Write(p []byte) (int, error) {
	c.WriteHeader(http.StatusOK)
	return c.body.Write(p)
}

func (c *captureWriter) statusCode() int {
	if c.status == 0 {
		return http.StatusOK
	}
	return c.status
}

func (c *captureWriter) reset() {
	c.status = 0
	c.body.Reset()
}

// observe buffers each response, turns a panic into a 500, logs one line,
// and rewrites any error body that is not already an envelope. This is the
// only place the envelope is enforced.
//
// The log line names the route pattern rather than the path, so OAuth codes
// and other query values never reach the logs.
func (s *Server) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		captured := &captureWriter{w: w}
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					// The panic value may hold moderation content; log only its type.
					slog.ErrorContext(r.Context(), "HTTP handler panicked",
						"panic_type", fmt.Sprintf("%T", recovered), "stack", string(debug.Stack()))
					captured.reset()
					writeError(captured, r, http.StatusInternalServerError, codeInternal, "The request could not be completed")
				}
			}()
			next.ServeHTTP(captured, r)
		}()

		status := captured.statusCode()
		route := routeFrom(r.Context())
		if route == "" {
			route = "unmatched"
		}
		level := slog.LevelInfo
		if status >= 500 {
			level = slog.LevelError
		} else if status >= 400 {
			level = slog.LevelWarn
		}
		requestID, correlationID := quack.TraceIDsFromContext(r.Context())
		slog.Log(r.Context(), level, "HTTP request completed",
			"request_id", requestID, "correlation_id", correlationID,
			"method", r.Method, "route", route, "status", status, "duration", time.Since(start))

		body := captured.body.Bytes()
		if status >= http.StatusBadRequest {
			body = normalizeError(r.Context(), status, body)
			w.Header().Set("Content-Type", jsonContentType)
		}
		w.Header().Del("Content-Length")
		w.WriteHeader(status)
		if len(body) > 0 {
			_, _ = w.Write(body)
		}
	})
}

// securityHeaders sets browser hardening headers suitable for a JSON API.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// cors allows credentialed requests only from the exact configured origins.
// A request from any other origin is rejected outright rather than merely
// left without CORS headers. Every OPTIONS request is answered here.
func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" {
			if !slices.Contains(s.cfg.API.CORSOrigins, origin) {
				writeError(w, r, http.StatusForbidden, codeOrigin, "request origin is not allowed")
				return
			}
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Idempotency-Key, X-CSRF-Token, X-Request-ID, X-Correlation-ID, X-Quack-Ops-Key")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			h.Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bodyLimit caps request bodies before any decoder reads them.
func (s *Server) bodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, s.cfg.API.MaxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
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

func (s *Server) trustedProxy(ip net.IP) bool {
	return slices.ContainsFunc(s.trustedProxies, func(n *net.IPNet) bool { return n.Contains(ip) })
}
