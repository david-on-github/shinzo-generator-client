package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func corsOKHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestCORSMiddleware_NoOriginsConfigured(t *testing.T) {
	h := corsMiddleware(nil, corsOKHandler())

	req := httptest.NewRequest(http.MethodOptions, "/health", nil)
	req.Header.Set("Origin", "https://explorer.shinzo.network")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "OPTIONS should reach the handler when CORS is disabled")
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORSMiddleware_AllowedOrigin(t *testing.T) {
	h := corsMiddleware([]string{"https://explorer.shinzo.network"}, corsOKHandler())

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Origin", "https://explorer.shinzo.network")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "https://explorer.shinzo.network", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "Origin", w.Header().Get("Vary"))
}

func TestCORSMiddleware_Preflight(t *testing.T) {
	h := corsMiddleware([]string{"http://localhost:3000"}, corsOKHandler())

	req := httptest.NewRequest(http.MethodOptions, "/api/v0/graphql", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "http://localhost:3000", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Contains(t, w.Header().Get("Access-Control-Allow-Methods"), "POST")
	assert.Equal(t, "3600", w.Header().Get("Access-Control-Max-Age"))
}

func TestCORSMiddleware_DisallowedOrigin(t *testing.T) {
	h := corsMiddleware([]string{"https://explorer.shinzo.network"}, corsOKHandler())

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "request still served; browser enforces the block")
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "Origin", w.Header().Get("Vary"))
}

func TestCORSMiddleware_Wildcard(t *testing.T) {
	h := corsMiddleware([]string{"*"}, corsOKHandler())

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Origin", "https://anything.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, "https://anything.example", w.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORSMiddleware_NoOriginHeader(t *testing.T) {
	h := corsMiddleware([]string{"https://explorer.shinzo.network"}, corsOKHandler())

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
}
