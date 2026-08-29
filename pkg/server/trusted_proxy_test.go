package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestForwardedHeaders_IgnoredUnlessTrustedProxy(t *testing.T) {
	untrusted := NewHealthServer(0, nil, "")
	trusted := NewHealthServer(0, nil, "", WithTrustedProxies([]string{"10.0.0.0/8"}))

	r := httptest.NewRequest(http.MethodGet, "http://node.example:8080/registration", nil)
	r.RemoteAddr = "203.0.113.5:4444"
	r.Header.Set("X-Forwarded-Host", "attacker.example")
	r.Header.Set("X-Forwarded-Proto", "https")

	assert.Empty(t, untrusted.withoutForwardedHeaders(r).Header.Get("X-Forwarded-Host"), "no trusted proxies: headers dropped")
	assert.Empty(t, trusted.withoutForwardedHeaders(r).Header.Get("X-Forwarded-Host"), "not from a trusted proxy: dropped")

	r.RemoteAddr = "10.1.2.3:4444"
	assert.Equal(t, "attacker.example", trusted.withoutForwardedHeaders(r).Header.Get("X-Forwarded-Host"), "from a trusted proxy: kept")
	assert.Equal(t, "https", trusted.withoutForwardedHeaders(r).Header.Get("X-Forwarded-Proto"))
}

func TestParseCIDRs_BareIPs(t *testing.T) {
	nets := parseCIDRs([]string{"10.0.0.1", " 192.168.0.0/16 ", "", "::1", "garbage"})
	assert.Len(t, nets, 3)
}
