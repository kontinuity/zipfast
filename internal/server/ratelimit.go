package server

import (
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"zipfast/internal/models"
)

// rlEntry is a per-key token-bucket limiter plus the last time it was touched,
// used by the background pruner to evict idle keys and bound memory.
type rlEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// rlStore holds the in-memory limiters keyed by (identity)+method+path. It is
// shared across requests for the lifetime of the App and guarded by a mutex.
// Access it via App.rlGetStore so it is initialized lazily and exactly once.
type rlStore struct {
	mu      sync.Mutex
	entries map[string]*rlEntry
	limit   rate.Limit
	burst   int

	// pruning bookkeeping.
	lastPrune time.Time
}

// rlConfig collects the tunables that govern pruning and the hard size cap.
const (
	// rlMaxEntries caps the map size; once exceeded the store is pruned of idle
	// keys immediately (and, if still over, fully reset) to avoid unbounded
	// growth from e.g. a flood of distinct client IPs.
	rlMaxEntries = 50000
	// rlIdleTTL is how long an unused key is kept before the pruner evicts it.
	rlIdleTTL = 10 * time.Minute
	// rlPruneInterval is the minimum spacing between opportunistic prunes.
	rlPruneInterval = 5 * time.Minute
)

// rlStoreInstance and rlStoreOnce back a single shared store per process. The
// store is keyed only by request identity (not by App), which is fine because a
// process runs one App; using a package-level singleton keeps RateLimit free of
// extra fields on App (owned by other files).
var (
	rlStoreInstance *rlStore
	rlStoreOnce     sync.Once
)

// rlGetStore returns the shared limiter store, configured from the App's
// ratelimit settings on first use.
func (a *App) rlGetStore() *rlStore {
	rlStoreOnce.Do(func() {
		limit, burst := rlRate(a.Cfg.Ratelimit.Max, a.Cfg.Ratelimit.Window)
		rlStoreInstance = &rlStore{
			entries:   make(map[string]*rlEntry),
			limit:     limit,
			burst:     burst,
			lastPrune: time.Now(),
		}
	})
	return rlStoreInstance
}

// rlRate converts the configured Max/Window into a token-bucket (Limit, burst).
//   - Window > 0: allow Max events per Window seconds, with Max as the burst so a
//     fresh client can spend its full allowance immediately.
//   - Window <= 0: fall back to per-second pacing using Max as both rate and burst.
//
// Max <= 0 is coerced to 1 to avoid a zero/negative bucket that would reject
// every request.
func rlRate(maxEvents, windowSeconds int) (rate.Limit, int) {
	if maxEvents <= 0 {
		maxEvents = 1
	}
	if windowSeconds > 0 {
		window := time.Duration(windowSeconds) * time.Second
		return rate.Every(window / time.Duration(maxEvents)), maxEvents
	}
	// Sane default: per-second with Max as burst.
	return rate.Limit(maxEvents), maxEvents
}

// RateLimit is chi-compatible middleware that throttles requests per identity
// (authenticated user id, else client IP) and per method+path. When
// Cfg.Ratelimit.Enabled is false it returns next unchanged (zero overhead).
//
// A request is exempt (passed straight through) when any of the following hold:
//   - it is a partial/chunked upload (carries the x-zipline-p-filename header);
//   - the identity or client IP is in Cfg.Ratelimit.AllowList;
//   - it is from an admin and Cfg.Ratelimit.AdminBypass is set.
//
// On exceeding the limit it responds 429 via App.Error.
func (a *App) RateLimit(next http.Handler) http.Handler {
	if !a.Cfg.Ratelimit.Enabled {
		return next
	}
	store := a.rlGetStore()
	allow := rlAllowSet(a.Cfg.Ratelimit.AllowList)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Partial/chunked uploads arrive as many small requests; never throttle them.
		if r.Header.Get("x-zipline-p-filename") != "" {
			next.ServeHTTP(w, r)
			return
		}

		ip := rlClientIP(r)

		// AllowList may name an IP. Skip the limiter entirely if so.
		if len(allow) > 0 && allow[ip] {
			next.ServeHTTP(w, r)
			return
		}

		// Resolve identity (and admin status) without a DB hit when possible: the
		// limiter key prefers a user id, falling back to the client IP.
		var identity string
		if user := a.authenticate(r); user != nil {
			if len(allow) > 0 && allow[user.ID] {
				next.ServeHTTP(w, r)
				return
			}
			if a.Cfg.Ratelimit.AdminBypass &&
				models.RoleRank(user.Role) >= models.RoleRank(models.RoleAdmin) {
				next.ServeHTTP(w, r)
				return
			}
			identity = "u:" + user.ID
		} else {
			identity = "ip:" + ip
		}

		key := identity + "|" + r.Method + "|" + r.URL.Path
		if !store.allow(key) {
			a.Error(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// allow records a hit for key and reports whether it is within the rate limit.
// It also performs opportunistic, low-cost pruning to keep the map bounded.
func (s *rlStore) allow(key string) bool {
	now := time.Now()

	s.mu.Lock()
	s.maybePruneLocked(now)

	e := s.entries[key]
	if e == nil {
		e = &rlEntry{limiter: rate.NewLimiter(s.limit, s.burst)}
		s.entries[key] = e
	}
	e.lastSeen = now
	ok := e.limiter.Allow()
	s.mu.Unlock()

	return ok
}

// maybePruneLocked evicts idle entries when the prune interval has elapsed or the
// map has grown past the cap. The caller must hold s.mu.
func (s *rlStore) maybePruneLocked(now time.Time) {
	overCap := len(s.entries) > rlMaxEntries
	if !overCap && now.Sub(s.lastPrune) < rlPruneInterval {
		return
	}
	s.lastPrune = now

	for k, e := range s.entries {
		if now.Sub(e.lastSeen) > rlIdleTTL {
			delete(s.entries, k)
		}
	}

	// Hard stop on pathological growth (e.g. an IP flood within the TTL window):
	// if still over the cap after evicting idle keys, reset the map outright.
	if len(s.entries) > rlMaxEntries {
		s.entries = make(map[string]*rlEntry)
	}
}

// rlClientIP extracts the client IP. middleware.RealIP (enabled when TrustProxy
// is set) already rewrites RemoteAddr from X-Forwarded-For / X-Real-IP, so the
// host portion of RemoteAddr is authoritative here. Falls back to the raw value
// when it has no port.
func rlClientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// rlAllowSet builds a set from the configured allow list for O(1) lookups.
func rlAllowSet(list []string) map[string]bool {
	if len(list) == 0 {
		return nil
	}
	m := make(map[string]bool, len(list))
	for _, v := range list {
		if v != "" {
			m[v] = true
		}
	}
	return m
}
