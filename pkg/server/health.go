// Package server provides HTTP handlers for the generator's health, metrics, registration,
// and schema endpoints.
//
// Error response formats differ by audience:
//   - Host-client endpoints (/api/v1/*) return structured JSON errors via writeJSONError,
//     using the errorResponse envelope {error, message}. These are consumed programmatically
//     by other generator clients that need machine-parseable error details.
//   - User-facing endpoints (/, /health, /registration, /metrics, /snapshots) use plain text
//     errors via http.Error. These serve browsers and operational dashboards where plain text
//     is simpler and sufficient.
package server

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shinzonetwork/shinzo-generator-client/pkg/logger"
	"github.com/shinzonetwork/shinzo-generator-client/pkg/snapshot"
	"github.com/sourcenetwork/defradb/node"
)

//go:embed health_status_page.html
var embeddedHealthStatusPageHTML string
var errIndexerNotAvailable = errors.New("indexer not available") //nolint:err113

const (
	// ServerUnhealthyStatus is the status string used when the server is unhealthy.
	ServerUnhealthyStatus = "unhealthy"

	// HealthCheckStaleThreshold is the duration after which a last-processed time is considered stale.
	HealthCheckStaleThreshold = 5 * time.Minute

	// DefraDBCheckTimeout is the timeout for checking DefraDB connectivity.
	DefraDBCheckTimeout = 5 * time.Second

	// ShinzoHubProtoAPIPort is the port for the ShinzoHub Cosmos LCD / protobuf REST API.
	ShinzoHubProtoAPIPort = 1317

	// ShinzoHubCosmosAPIPort is the port for the ShinzoHub Cosmos RPC API.
	ShinzoHubCosmosAPIPort = 25567
)

// ShinzoHubAPIURL builds a full base URL from a hostname and port.
// Returns an empty string when hostname is empty so callers can skip hub queries gracefully.
func ShinzoHubAPIURL(hostname string, port int) string {
	if hostname == "" {
		return ""
	}
	return fmt.Sprintf("http://%s:%d", hostname, port)
}

// HealthServer provides HTTP endpoints for health checks and metrics.
type HealthServer struct {
	server               *http.Server
	mux                  *http.ServeMux
	indexer              HealthChecker
	defraURL             string
	snapshotter          *snapshot.Snapshotter
	defraNode            *node.Node
	startTime            time.Time
	healthStatusPagePath string
	querySnapshotSigsFn  func(ctx context.Context, n *node.Node) (map[string]*snapshot.SnapshotSignatureData, error)
	shinzoHubRESTBase    string // full base URL injected into the health page template, e.g. "http://testnet.shinzo.network:1317"
	tlsCertFile          string
	tlsKeyFile           string
	trustedProxies       []*net.IPNet
	passphraseSource     string
}

// Option configures optional HealthServer behaviour.
type Option func(*serverOptions)

type serverOptions struct {
	allowedOrigins   []string
	tlsCertFile      string
	tlsKeyFile       string
	trustedProxies   []string
	passphraseSource string
}

// WithAllowedOrigins enables CORS for the given browser origins ("*" allows any).
// With no origins configured, no CORS headers are sent.
func WithAllowedOrigins(origins []string) Option {
	return func(o *serverOptions) { o.allowedOrigins = origins }
}

// WithTLS serves HTTPS using the given PEM certificate and key files.
func WithTLS(certFile, keyFile string) Option {
	return func(o *serverOptions) {
		o.tlsCertFile = certFile
		o.tlsKeyFile = keyFile
	}
}

// HealthChecker interface for checking indexer health.
type HealthChecker interface {
	IsHealthy() bool
	GetCurrentBlock() int64
	GetLastProcessedTime() time.Time
	GetPeerInfo() (*P2PInfo, error)
	GetSourceChainInfo() (string, uint64)
	SignRegistrationMessage(message string) (DefraPKRegistration, error)
	SignMessages(message string) (DefraPKRegistration, PeerIDRegistration, error)
}

// P2PInfo represents DefraDB P2P network information.
type P2PInfo struct {
	Enabled  bool       `json:"enabled"`
	Self     *PeerInfo  `json:"self,omitempty"`
	PeerInfo []PeerInfo `json:"peers"`
	// Announce is the operator-configured public P2P address (without /p2p/…),
	// used in preference to anything derived from listen addresses or the request.
	Announce string `json:"announce,omitempty"`
}

// PeerInfo contains address and identity information for a DefraDB P2P peer.
type PeerInfo struct {
	ID        string   `json:"id"`
	Addresses []string `json:"addresses"`
	PublicKey string   `json:"public_key,omitempty"`
}

// HealthResponse represents the health check response.
type HealthResponse struct {
	Status           string               `json:"status"`
	Timestamp        time.Time            `json:"timestamp"`
	CurrentBlock     int64                `json:"current_block,omitempty"`
	LastProcessed    time.Time            `json:"last_processed,omitzero"`
	DefraDBConnected bool                 `json:"defradb_connected"`
	Uptime           string               `json:"uptime"`
	UptimeSeconds    float64              `json:"uptime_seconds"`
	P2P              *P2PInfo             `json:"p2p,omitempty"`
	Registration     *DisplayRegistration `json:"registration,omitempty"`
	KeyPassphrase    string               `json:"key_passphrase,omitempty"` // "provided" | "generated"
	BuildTags        string               `json:"build_tags,omitempty"`
	SchemaType       string               `json:"schema_type,omitempty"`
}

// MetricsResponse represents basic metrics.
type MetricsResponse struct {
	BlocksProcessed   int64     `json:"blocks_processed"`
	CurrentBlock      int64     `json:"current_block"`
	LastProcessedTime time.Time `json:"last_processed_time"`
	Uptime            string    `json:"uptime"`
}

// NewHealthServer creates a new health server.
func NewHealthServer(port int, indexer HealthChecker, defraURL string, opts ...Option) *HealthServer {
	var o serverOptions
	for _, apply := range opts {
		apply(&o)
	}

	// Config values are commonly host:port; the proxy and readiness checks need a scheme.
	if defraURL != "" && !strings.Contains(defraURL, "://") {
		defraURL = "http://" + defraURL
	}

	mux := http.NewServeMux()

	hs := &HealthServer{
		server: &http.Server{
			Addr:         fmt.Sprintf(":%d", port),
			Handler:      corsMiddleware(o.allowedOrigins, mux),
			ReadTimeout:  10 * time.Second, //nolint:mnd
			WriteTimeout: 5 * time.Minute,  //nolint:mnd // large snapshot files need time to transfer.
		},
		mux:                  mux,
		indexer:              indexer,
		defraURL:             defraURL,
		startTime:            time.Now(),
		healthStatusPagePath: "pkg/server/health_status_page.html",
		querySnapshotSigsFn:  snapshot.QuerySnapshotSignatures,
		tlsCertFile:          o.tlsCertFile,
		tlsKeyFile:           o.tlsKeyFile,
		trustedProxies:       parseCIDRs(o.trustedProxies),
		passphraseSource:     o.passphraseSource,
	}

	// Register routes
	mux.HandleFunc("/health", hs.healthHandler)
	mux.HandleFunc("/registration", hs.registrationHandler)
	mux.HandleFunc("/api/v0/registration", hs.registrationHandler) // backward-compatible alias
	mux.HandleFunc("/registration-app", hs.registrationAppHandler)
	mux.HandleFunc("/metrics", hs.metricsHandler)
	mux.HandleFunc("GET /{$}", hs.rootHandler)

	// Expose the DefraDB API (GraphQL etc.) on this port so clients need a
	// single origin. The exact /api/v0/registration pattern above wins over
	// this prefix; /api/v1/* (schema) is a different prefix and unaffected.
	if proxy := newDefraProxy(defraURL); proxy != nil {
		mux.Handle("/api/v0/", proxy)
	}

	return hs
}

// newDefraProxy returns a reverse proxy to the DefraDB HTTP API, or nil if no
// DefraDB URL is configured.
func newDefraProxy(defraURL string) http.Handler {
	if defraURL == "" {
		return nil
	}
	if !strings.Contains(defraURL, "://") {
		defraURL = "http://" + defraURL // config values are commonly host:port
	}
	target, err := url.Parse(defraURL)
	if err != nil || target.Host == "" {
		logger.Sugar.Warnf("Not proxying DefraDB API: invalid defra URL %q", defraURL)
		return nil
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		logger.Sugar.Warnf("DefraDB proxy error: %v", err)
		http.Error(w, "DefraDB unavailable", http.StatusBadGateway)
	}
	return proxy
}

// SetSnapshotter registers the snapshot provider and enables snapshot HTTP endpoints.
func (hs *HealthServer) SetSnapshotter(s *snapshot.Snapshotter) {
	hs.snapshotter = s
	hs.mux.HandleFunc("/snapshots", hs.snapshotsListHandler)
	hs.mux.HandleFunc("/snapshots/", hs.snapshotDownloadHandler)
}

// SetDefraNode sets the DefraDB node reference for import operations.
func (hs *HealthServer) SetDefraNode(n *node.Node) {
	hs.defraNode = n
	hs.mux.HandleFunc("/snapshots/import", hs.snapshotImportHandler)
}

// SetShinzoHubRESTBase sets the ShinzoHub REST base URL injected into the health status page.
// url should be the full base URL including scheme and port, e.g. "http://testnet.shinzo.network:1317".
// Use shinzoHubAPIURL to build it from a hostname config value.
func (hs *HealthServer) SetShinzoHubRESTBase(url string) {
	hs.shinzoHubRESTBase = url
}

// Start starts the health server, serving TLS if a certificate was configured.
func (hs *HealthServer) Start() error {
	if hs.tlsCertFile != "" || hs.tlsKeyFile != "" {
		logger.Sugar.Infof("Starting health server (TLS) on %s", hs.server.Addr)
		return hs.server.ListenAndServeTLS(hs.tlsCertFile, hs.tlsKeyFile)
	}
	logger.Sugar.Infof("Starting health server on %s", hs.server.Addr)
	return hs.server.ListenAndServe()
}

// Stop gracefully stops the health server.
func (hs *HealthServer) Stop(ctx context.Context) error {
	logger.Sugar.Info("Stopping health server...")
	return hs.server.Shutdown(ctx)
}

// healthHandler handles liveness probe requests.
func (hs *HealthServer) healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Content negotiation: Default to HTML for browsers, only serve JSON if explicitly requested.
	accept := r.Header.Get("Accept")
	acceptLower := strings.ToLower(accept)

	uptime := time.Since(hs.startTime)

	// Serve JSON only if explicitly requested (Accept contains application/json and not text/html).
	// Otherwise, default to HTML for browser requests.
	if strings.Contains(acceptLower, "text/html") && !strings.Contains(acceptLower, "application/json") {
		// Default to HTML (browser request or Accept header includes text/html).
		htmlContent := hs.getHealthStatusPageHTML()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(htmlContent)
		return
	}
	// Serve JSON response.
	response := HealthResponse{
		Status:           "healthy",
		Timestamp:        time.Now(),
		DefraDBConnected: hs.checkDefraDB(),
		Uptime:           uptime.String(),
		UptimeSeconds:    uptime.Seconds(),
	}
	response.KeyPassphrase = hs.passphraseSource

	if hs.indexer != nil {
		response.CurrentBlock = hs.indexer.GetCurrentBlock()
		response.LastProcessed = hs.indexer.GetLastProcessedTime()
		p2p, _ := hs.indexer.GetPeerInfo()
		response.P2P = p2p

		if !hs.indexer.IsHealthy() {
			response.Status = ServerUnhealthyStatus
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if response.Status == ServerUnhealthyStatus {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(response)
}

// registrationHandler handles readiness probe requests.
func (hs *HealthServer) registrationHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check if indexer is ready (has processed at least one block recently).
	ready := true
	if hs.indexer != nil {
		lastProcessed := hs.indexer.GetLastProcessedTime()
		if time.Since(lastProcessed) > HealthCheckStaleThreshold && !lastProcessed.IsZero() {
			ready = false
		}
	}

	// Check DefraDB connectivity.
	if !hs.checkDefraDB() {
		ready = false
	}

	uptime := time.Since(hs.startTime)
	response := HealthResponse{
		Status:           "ready",
		Timestamp:        time.Now(),
		DefraDBConnected: hs.checkDefraDB(),
		Uptime:           uptime.String(),
		UptimeSeconds:    uptime.Seconds(),
	}

	if hs.indexer != nil {
		response.CurrentBlock = hs.indexer.GetCurrentBlock()
		response.LastProcessed = hs.indexer.GetLastProcessedTime()
		p2p, err := hs.indexer.GetPeerInfo()
		response.P2P = p2p
		if err != nil {
			response.Status = ServerUnhealthyStatus
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}

		registration, _ := hs.getRegistrationData(r)
		response.Registration = registration
	}

	if !ready {
		response.Status = "not ready"
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// metricsHandler provides basic metrics in JSON format.
func (hs *HealthServer) metricsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	metrics := MetricsResponse{
		Uptime: time.Since(hs.startTime).String(),
	}

	if hs.indexer != nil {
		metrics.CurrentBlock = hs.indexer.GetCurrentBlock()
		metrics.LastProcessedTime = hs.indexer.GetLastProcessedTime()
		metrics.BlocksProcessed = hs.indexer.GetCurrentBlock() // Simplified
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(metrics)
}

// rootHandler handles root requests.
func (hs *HealthServer) rootHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	response := map[string]any{
		"service":   "Shinzo Network Indexer",
		"version":   "1.0.0",
		"status":    "running",
		"timestamp": time.Now(),
		"endpoints": []string{
			"/health 	      - Health probe",
			"/registration  - Registration information",
			"/registration-app - Registration webapp",
			"/metrics 	    - Basic metrics",
			"/snapshots     - List available snapshots",
			"/snapshots/:id - Download a snapshot file",
			"/api/v1/schema           - Full GraphQL schema SDL",
			"/api/v1/schema/{name}    - Collection schema SDL",
			"/api/v1/schema/collections - Collections metadata",
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// snapshotListEntry extends SnapshotInfo with inline signature data.
type snapshotListEntry struct {
	snapshot.SnapshotInfo

	Signed    bool                            `json:"signed"`
	Signature *snapshot.SnapshotSignatureData `json:"signature,omitempty"`
}

// snapshotsListHandler returns a JSON list of available snapshot files with inline signatures.
func (hs *HealthServer) snapshotsListHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if hs.snapshotter == nil {
		http.Error(w, "Snapshots not enabled", http.StatusNotFound)
		return
	}

	infos := hs.snapshotter.ListSnapshots()

	// Query DefraDB for all snapshot signatures, keyed by filename.
	var sigs map[string]*snapshot.SnapshotSignatureData
	if hs.defraNode != nil {
		var err error
		sigs, err = hs.querySnapshotSigsFn(r.Context(), hs.defraNode)
		if err != nil {
			logger.Sugar.Warnf("Failed to query snapshot signatures: %v", err)
		}
	}

	entries := make([]snapshotListEntry, len(infos))
	for i, info := range infos {
		sig := sigs[info.Filename]
		entries[i] = snapshotListEntry{
			SnapshotInfo: info,
			Signed:       sig != nil,
			Signature:    sig,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"snapshots": entries,
		"count":     len(entries),
	})
}

// snapshotDownloadHandler serves a snapshot file by name.
// URL: /snapshots/{filename} — serves .jsonl.gz snapshot file.
func (hs *HealthServer) snapshotDownloadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if hs.snapshotter == nil {
		http.Error(w, "Snapshots not enabled", http.StatusNotFound)
		return
	}

	filename := strings.TrimPrefix(r.URL.Path, "/snapshots/")
	if filename == "" {
		hs.snapshotsListHandler(w, r)
		return
	}

	filePath := hs.snapshotter.GetSnapshotPath(filename)
	if filePath == "" {
		http.Error(w, "Snapshot not found", http.StatusNotFound)
		return
	}

	f, err := os.Open(filepath.Clean(filePath))
	if err != nil {
		http.Error(w, "Failed to open file", http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()

	stat, err := f.Stat()
	if err != nil {
		http.Error(w, "Failed to stat file", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", stat.Size()))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))

	written, err := io.Copy(w, f)
	if err != nil {
		logger.Sugar.Errorf("Snapshot download error for %s: %v (wrote %d/%d bytes)", filename, err, written, stat.Size())
	} else {
		logger.Sugar.Infof("Snapshot served: %s (%d bytes)", filename, written)
	}
}

// snapshotImportHandler imports a snapshot file by name.
// POST /snapshots/import?file=snapshot_X_Y.kvsnap.gz.
func (hs *HealthServer) snapshotImportHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if hs.defraNode == nil {
		http.Error(w, "Import not available (no embedded DefraDB)", http.StatusServiceUnavailable)
		return
	}
	if hs.snapshotter == nil {
		http.Error(w, "Snapshots not enabled", http.StatusNotFound)
		return
	}

	filename := r.URL.Query().Get("file")
	if filename == "" {
		http.Error(w, "Missing 'file' query parameter", http.StatusBadRequest)
		return
	}

	filePath := hs.snapshotter.GetSnapshotPath(filename)
	if filePath == "" {
		http.Error(w, "Snapshot not found", http.StatusNotFound)
		return
	}

	result, err := snapshot.ImportKV(r.Context(), hs.defraNode, filePath)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":  err.Error(),
			"result": result,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "ok",
		"result": result,
	})
}

// checkDefraDB checks if DefraDB is accessible.
func (hs *HealthServer) checkDefraDB() bool {
	if hs.defraURL == "" {
		return true // Embedded mode, assume healthy.
	}

	if strings.Contains(hs.defraURL, "localhost") || strings.Contains(hs.defraURL, "127.0.0.1") {
		return true
	}

	client := &http.Client{Timeout: DefraDBCheckTimeout}
	resp, err := client.Get(hs.defraURL + "/api/v0/graphql")
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()

	return resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusBadRequest // GraphQL endpoint returns 400 for GET.
}

// normalizeHex ensures a string is represented as a 0x-prefixed hex string.
// If the string is empty, it is returned unchanged.
func normalizeHex(s string) string {
	if s == "" {
		return s
	}
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		// Normalize any 0X to 0x for consistency.
		return "0x" + s[2:]
	}
	return "0x" + s
}

// getHealthStatusPageHTML reads the HTML template and renders it with runtime config values.
func (hs *HealthServer) getHealthStatusPageHTML() []byte {
	raw := hs.loadHealthStatusPageTemplate()
	rendered := strings.ReplaceAll(string(raw), "{{SHINZOHUB_REST_BASE}}", hs.shinzoHubRESTBase)
	return []byte(rendered)
}

// loadHealthStatusPageTemplate reads the raw HTML template from disk at runtime, falling back to
// the embedded version. Disk reads allow hot-reloading during development without rebuilding.
func (hs *HealthServer) loadHealthStatusPageTemplate() []byte {
	possiblePaths := []string{
		hs.healthStatusPagePath,
		filepath.Join(".", "health_status_page.html"),
	}

	for _, path := range possiblePaths {
		if data, err := os.ReadFile(filepath.Clean(path)); err == nil {
			logger.Sugar.Debugf("Loaded health status page from: %s", path)
			return data
		}
	}

	logger.Sugar.Debug("Using embedded health status page")
	return []byte(embeddedHealthStatusPageHTML)
}

// trustsProxy reports whether forwarded headers from this request may be
// believed: only when the request came from a configured trusted proxy. With
// no proxies configured (the default), X-Forwarded-* is ignored entirely —
// before the nginx sidecar was removed, nginx overwrote those headers, so a
// client could never set them; this keeps that property without nginx.
func (hs *HealthServer) trustsProxy(r *http.Request) bool {
	if len(hs.trustedProxies) == 0 || r == nil {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, n := range hs.trustedProxies {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// withoutForwardedHeaders returns r as-is when the sender is a trusted proxy,
// otherwise a shallow clone with the X-Forwarded-* headers removed.
func (hs *HealthServer) withoutForwardedHeaders(r *http.Request) *http.Request {
	if hs.trustsProxy(r) {
		return r
	}
	c := r.Clone(r.Context())
	for _, h := range []string{"X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-For"} {
		c.Header.Del(h)
	}
	return c
}

// WithTrustedProxies sets the CIDRs whose X-Forwarded-* headers are honoured.
func WithTrustedProxies(cidrs []string) Option {
	return func(o *serverOptions) { o.trustedProxies = cidrs }
}

// WithPassphraseSource records whether the node's key passphrase was provided
// by the operator or generated on first run, for /health.
func WithPassphraseSource(source string) Option {
	return func(o *serverOptions) { o.passphraseSource = source }
}

func parseCIDRs(cidrs []string) []*net.IPNet {
	var out []*net.IPNet
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if !strings.Contains(c, "/") { // bare IP
			if strings.Contains(c, ":") {
				c += "/128"
			} else {
				c += "/32"
			}
		}
		if _, n, err := net.ParseCIDR(c); err == nil {
			out = append(out, n)
		}
	}
	return out
}
