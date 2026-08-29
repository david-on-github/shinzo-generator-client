package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefraProxy_ForwardsAPIRequests(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v0/graphql", r.URL.Path)
		_, _ = io.WriteString(w, `{"data":{}}`)
	}))
	defer upstream.Close()

	hs := NewHealthServer(0, nil, upstream.URL, WithAllowedOrigins([]string{"http://localhost:3000"}))

	req := httptest.NewRequest(http.MethodPost, "/api/v0/graphql", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	w := httptest.NewRecorder()
	hs.server.Handler.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, `{"data":{}}`, w.Body.String())
	assert.Equal(t, "http://localhost:3000", w.Header().Get("Access-Control-Allow-Origin"))
}

func TestDefraProxy_RegistrationAliasNotProxied(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("registration alias must not reach DefraDB")
	}))
	defer upstream.Close()

	hs := NewHealthServer(0, nil, upstream.URL)

	req := httptest.NewRequest(http.MethodGet, "/api/v0/registration", nil)
	w := httptest.NewRecorder()
	hs.server.Handler.ServeHTTP(w, req)

	assert.NotEqual(t, http.StatusNotFound, w.Code)
}

func TestDefraProxy_NoDefraURL(t *testing.T) {
	hs := NewHealthServer(0, nil, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v0/graphql", nil)
	w := httptest.NewRecorder()
	hs.server.Handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
