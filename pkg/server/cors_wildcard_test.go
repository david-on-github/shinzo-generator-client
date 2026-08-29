package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func allowOrigin(t *testing.T, patterns []string, origin string) string {
	t.Helper()
	h := corsMiddleware(patterns, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Origin", origin)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Header().Get("Access-Control-Allow-Origin")
}

func TestCORSWildcard(t *testing.T) {
	patterns := []string{"https://*.shinzo.network"}
	cases := map[string]bool{
		"https://explorer.shinzo.network":      true,
		"https://studio.shinzo.network":        true,
		"https://a.b.shinzo.network":           true,
		"HTTPS://Explorer.Shinzo.Network":      true,
		"https://shinzo.network":               false, // apex is not a subdomain
		"http://explorer.shinzo.network":       false, // scheme pinned
		"https://evil-shinzo.network":          false, // lookalike
		"https://shinzo.network.evil.example":  false,
		"https://explorer.shinzo.network:8443": false, // port not in pattern
	}
	for origin, want := range cases {
		got := allowOrigin(t, patterns, origin)
		if want {
			assert.Equal(t, origin, got, origin)
		} else {
			assert.Empty(t, got, origin)
		}
	}
}

func TestCORSWildcard_MixedWithExact(t *testing.T) {
	patterns := []string{"https://*.shinzo.network", "http://localhost:3000"}
	assert.Equal(t, "http://localhost:3000", allowOrigin(t, patterns, "http://localhost:3000"))
	assert.Equal(t, "https://explorer.shinzo.network", allowOrigin(t, patterns, "https://explorer.shinzo.network"))
	assert.Empty(t, allowOrigin(t, patterns, "http://localhost:3001"))
}
