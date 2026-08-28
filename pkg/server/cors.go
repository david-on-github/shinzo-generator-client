package server

import (
	"net/http"
	"strings"
)

// corsMiddleware adds CORS headers for requests whose Origin matches one of
// allowedOrigins and short-circuits preflight (OPTIONS) requests.
//
// Entries may be exact origins ("https://app.example.com"), wildcard
// subdomains ("https://*.example.com" — any subdomain, same scheme, not the
// apex), or "*" for any origin. With an empty list the middleware is a no-op,
// so nodes that are not called directly from a browser send no CORS headers.
func corsMiddleware(allowedOrigins []string, next http.Handler) http.Handler {
	if len(allowedOrigins) == 0 {
		return next
	}

	allowAll := false
	allowed := make(map[string]struct{}, len(allowedOrigins))
	var wildcards []string // "https://*.example.com" -> "https://.example.com" suffix match
	for _, o := range allowedOrigins {
		o = strings.ToLower(strings.TrimRight(strings.TrimSpace(o), "/"))
		switch {
		case o == "*":
			allowAll = true
		case o == "":
		case strings.Contains(o, "://*."):
			wildcards = append(wildcards, strings.Replace(o, "://*.", "://.", 1))
		default:
			allowed[o] = struct{}{}
		}
	}
	originAllowed := func(origin string) bool {
		if allowAll {
			return true
		}
		o := strings.ToLower(origin)
		if _, ok := allowed[o]; ok {
			return true
		}
		for _, w := range wildcards {
			// scheme must match exactly; host must be a proper subdomain (not the apex,
			// not a lookalike like "evil-example.com"); no port unless the pattern has one.
			scheme, suffix, _ := strings.Cut(w, "://")
			oScheme, oHost, ok := strings.Cut(o, "://")
			if ok && oScheme == scheme && strings.HasSuffix(oHost, suffix) && len(oHost) > len(suffix) {
				return true
			}
		}
		return false
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The response varies by Origin whether or not this request carried one,
		// so caches never serve an allowed-origin response to a different origin.
		w.Header().Add("Vary", "Origin")

		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}

		if !originAllowed(origin) {
			// Not an allowed origin: serve the request normally with no CORS
			// headers; the browser enforces the block.
			next.ServeHTTP(w, r)
			return
		}

		h := w.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, Origin")
		h.Set("Access-Control-Max-Age", "3600")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
