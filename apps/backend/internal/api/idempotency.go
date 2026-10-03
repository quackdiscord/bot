package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	idempotencyKeyHeader = "Idempotency-Key"
	// maxIdempotentRequestBytes bounds the body read for the fingerprint.
	maxIdempotentRequestBytes = 4 << 20
	// maxIdempotentResponseBytes bounds a stored response. A larger response
	// fails the request rather than going unrecorded.
	maxIdempotentResponseBytes = 64 << 10
)

type idempotencyState string

const (
	idempotencyConflict   idempotencyState = "conflict"
	idempotencyAcquired   idempotencyState = "acquired"
	idempotencyInProgress idempotencyState = "in_progress"
	idempotencyComplete   idempotencyState = "complete"
)

// idempotencyResult is what begin found for a key: a fresh lease, a request
// still running, a stored response, or a conflicting fingerprint.
type idempotencyResult struct {
	state      idempotencyState
	leaseToken string
	statusCode int
	body       []byte
}

// idempotencyStore records write results in Redis. Each key holds a fenced
// lease while the write runs, then the response, so a retried request
// replays the original result instead of running twice.
type idempotencyStore struct {
	client redis.UniversalClient
	prefix string
}

// beginScript returns the key's state, or creates an in-progress lease if
// the key is new. A different request fingerprint is a conflict.
var beginScript = redis.NewScript(`
if redis.call("EXISTS", KEYS[1]) == 1 then
  if (redis.call("HGET", KEYS[1], "fingerprint") or "") ~= ARGV[3] then return {"conflict", "", "0", "", redis.call("PTTL", KEYS[1])} end
  return {redis.call("HGET", KEYS[1], "state"), "", redis.call("HGET", KEYS[1], "status") or "0", redis.call("HGET", KEYS[1], "body") or "", redis.call("PTTL", KEYS[1])}
end
redis.call("HSET", KEYS[1], "state", "in_progress", "token", ARGV[1], "status", "0", "body", "", "fingerprint", ARGV[3])
redis.call("PEXPIRE", KEYS[1], ARGV[2])
return {"acquired", ARGV[1], "0", "", redis.call("PTTL", KEYS[1])}
`)

// completeScript stores the response only if the caller still holds the lease.
var completeScript = redis.NewScript(`
if redis.call("EXISTS", KEYS[1]) == 0 then return -1 end
if redis.call("HGET", KEYS[1], "token") ~= ARGV[1] then return -2 end
if redis.call("HGET", KEYS[1], "state") ~= "in_progress" then return -3 end
redis.call("HSET", KEYS[1], "state", "complete", "status", ARGV[2], "body", ARGV[3])
redis.call("PEXPIRE", KEYS[1], ARGV[4])
return 1
`)

func newIdempotencyStore(client redis.UniversalClient) *idempotencyStore {
	return &idempotencyStore{client: client, prefix: "http:idempotency:"}
}

func (s *idempotencyStore) begin(ctx context.Context, scope, key string, ttl time.Duration, fingerprint string) (idempotencyResult, error) {
	token, err := randomToken()
	if err != nil {
		return idempotencyResult{}, fmt.Errorf("generate idempotency lease: %w", err)
	}
	ttlMillis := max(ttl.Milliseconds(), 1)
	raw, err := beginScript.Run(ctx, s.client, []string{s.key(scope, key)}, token, ttlMillis, fingerprint).Result()
	if err != nil {
		return idempotencyResult{}, fmt.Errorf("%w: begin idempotency: %v", errUnavailable, err)
	}
	values, ok := raw.([]any)
	if !ok || len(values) != 5 {
		return idempotencyResult{}, fmt.Errorf("%w: invalid idempotency response", errUnavailable)
	}
	state, err := redisString(values[0])
	if err != nil {
		return idempotencyResult{}, fmt.Errorf("%w: invalid idempotency state", errUnavailable)
	}
	leaseToken, _ := redisString(values[1])
	status, err := redisInt64(values[2])
	if err != nil {
		return idempotencyResult{}, fmt.Errorf("%w: invalid idempotency status", errUnavailable)
	}
	body, _ := redisString(values[3])
	return idempotencyResult{
		state:      idempotencyState(state),
		leaseToken: leaseToken,
		statusCode: int(status),
		body:       []byte(body),
	}, nil
}

func (s *idempotencyStore) complete(ctx context.Context, scope, key, leaseToken string, status int, body []byte, ttl time.Duration) error {
	if len(body) > maxIdempotentResponseBytes {
		return fmt.Errorf("idempotency response exceeds %d bytes", maxIdempotentResponseBytes)
	}
	ttlMillis := max(ttl.Milliseconds(), 1)
	result, err := completeScript.Run(ctx, s.client, []string{s.key(scope, key)}, leaseToken, status, body, ttlMillis).Int64()
	if err != nil {
		return fmt.Errorf("%w: complete idempotency: %v", errUnavailable, err)
	}
	switch result {
	case 1:
		return nil
	case -1:
		return errors.New("idempotency lease expired")
	case -2:
		return errors.New("idempotency lease ownership lost")
	default:
		return errors.New("idempotency operation already completed")
	}
}

func (s *idempotencyStore) key(scope, key string) string {
	return hashedKey(s.prefix, scope+"\x00"+key)
}

func randomToken() (string, error) {
	var body [32]byte
	if _, err := rand.Read(body[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(body[:]), nil
}

func redisString(value any) (string, error) {
	switch value := value.(type) {
	case string:
		return value, nil
	case []byte:
		return string(value), nil
	case nil:
		return "", nil
	default:
		return "", fmt.Errorf("unexpected Redis string type %T", value)
	}
}

// idempotent makes a write safe to retry. It requires an Idempotency-Key,
// scoped to class, subject, and the request target, and fingerprints the
// query, content type, and body. The first request runs; a retry gets the
// stored response with Idempotency-Replayed: true; a retry while the first is
// running gets 409 with Retry-After; reusing the key for a different request
// gets 409.
//
// Put it after authorization: a replay must still pass the caller's current
// permissions.
func (s *Server) idempotent(class string, subject func(*http.Request) string) middleware {
	ttl := s.cfg.API.IdempotencyTTL
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := strings.TrimSpace(r.Header.Get(idempotencyKeyHeader))
			if key == "" || len(key) > 256 {
				writeError(w, r, http.StatusBadRequest, codeValidation, "a valid Idempotency-Key header is required")
				return
			}
			scope := class + ":" + subject(r) + ":" + r.Method + ":" + r.URL.EscapedPath()
			body, err := io.ReadAll(io.LimitReader(r.Body, maxIdempotentRequestBytes+1))
			if err != nil || len(body) > maxIdempotentRequestBytes {
				writeError(w, r, http.StatusBadRequest, codeValidation, "request body is unavailable or too large")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			digest := sha256.Sum256(append([]byte(r.URL.RawQuery+"\x00"+mediaType(r)+"\x00"), body...))

			result, err := s.idempotency.begin(r.Context(), scope, key, ttl, hex.EncodeToString(digest[:]))
			if err != nil {
				writeError(w, r, http.StatusServiceUnavailable, codeDependency, "idempotency service unavailable")
				return
			}
			switch result.state {
			case idempotencyAcquired:
			case idempotencyConflict:
				writeError(w, r, http.StatusConflict, codeConflict, "Idempotency-Key was already used with a different request")
				return
			case idempotencyInProgress:
				w.Header().Set("Retry-After", "1")
				writeError(w, r, http.StatusConflict, codeConflict, "an identical request is still in progress")
				return
			case idempotencyComplete:
				w.Header().Set("Idempotency-Replayed", "true")
				w.Header().Set("Content-Type", jsonContentType)
				w.WriteHeader(result.statusCode)
				_, _ = w.Write(result.body)
				return
			default:
				writeError(w, r, http.StatusServiceUnavailable, codeDependency, "idempotency service unavailable")
				return
			}

			captured := &captureWriter{w: w}
			next.ServeHTTP(captured, r)
			status, stored := captured.statusCode(), captured.body.Bytes()
			if status >= http.StatusBadRequest {
				// Store the envelope, not whatever raw error text a module wrote.
				stored = normalizeError(r.Context(), status, stored)
			}
			if err := s.idempotency.complete(r.Context(), scope, key, result.leaseToken, status, stored, ttl); err != nil {
				writeError(w, r, http.StatusServiceUnavailable, codeDependency, "idempotency result could not be recorded")
				return
			}
			w.WriteHeader(status)
			_, _ = w.Write(stored)
		})
	}
}

// mediaType is the Content-Type without parameters.
func mediaType(r *http.Request) string {
	contentType := r.Header.Get("Content-Type")
	if i := strings.IndexAny(contentType, " ;"); i >= 0 {
		return contentType[:i]
	}
	return contentType
}
