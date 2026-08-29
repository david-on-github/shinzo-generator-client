package server

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConnectionString_AnnounceWins(t *testing.T) {
	r := httptest.NewRequest("GET", "http://203.0.113.9:8080/registration", nil)
	p2p := &P2PInfo{Self: &PeerInfo{ID: "12D3KooWTEST", Addresses: []string{"/ip4/0.0.0.0/tcp/9171"}}}
	p2p.Announce = "/dns4/node.example/tcp/19171"
	assert.Equal(t, "/dns4/node.example/tcp/19171/p2p/12D3KooWTEST", deriveConnectionString(r, p2p))
}
