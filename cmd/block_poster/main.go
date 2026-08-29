package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shinzonetwork/shinzo-generator-client/config"
	"github.com/shinzonetwork/shinzo-generator-client/pkg/indexer"
	"github.com/shinzonetwork/shinzo-generator-client/pkg/snapshot"
)

// version is set by the build (-ldflags "-X main.version=vX.Y.Z").
var version = "dev" //nolint:gochecknoglobals

const (
	exitUsage       = 2 // conventional exit code for bad command-line usage
	defaultHTTPPort = 8080
	clientTimeout   = 5 * time.Second
)

const usage = `Shinzo generator — indexes a source chain and serves primitives to hosts.

Usage:
  block_poster [run] [flags]     start the node (default)
  block_poster health [--port]   exit 0 if a local node reports healthy
  block_poster id     [--port]   print the local node's peer ID and connection string
  block_poster verify <snapshot.jsonl.gz>...
  block_poster version

Flags for run (each mirrors an environment variable):
  -config       path     config file           (default: ./config/config.yaml if present, else built-in)
  --data-dir    path     all node state        (SHINZO_DATA_DIR; default: ~/.shinzo/generator)
  --passphrase  string   key passphrase        (SHINZO_KEY_PASSPHRASE; default: generated on first run)

Required environment: GETH_RPC_URL, GETH_WS_URL (and GETH_API_KEY / GETH_API_KEY_TYPE if your provider needs them).
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := "run"
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "run":
		return runNode(args)
	case "verify": //nolint:goconst
		return verifySnapshots(args, os.Stdout, os.Stderr)
	case "health":
		return healthCmd(args)
	case "id":
		return idCmd(args)
	case "version":
		fmt.Println(version)
		return nil
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(exitUsage)
		return nil
	}
}

func runNode(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.Usage = func() { fmt.Print(usage) }
	configPath := fs.String("config", "", "Path to configuration file")
	dataDir := fs.String("data-dir", "", "data directory")
	passphrase := fs.String("passphrase", "", "key passphrase")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("failed to parse flags: %w", err)
	}
	// Flags are sugar over the environment so there is exactly one config path.
	if *dataDir != "" {
		_ = os.Setenv("SHINZO_DATA_DIR", *dataDir)
	}
	if *passphrase != "" {
		_ = os.Setenv("SHINZO_KEY_PASSPHRASE", *passphrase)
	}

	// Load configuration
	cfg, err := config.Load(findConfigFile(*configPath))
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	printBanner(cfg)

	// Create and start indexer
	chainIndexer, err := indexer.CreateIndexer(cfg)
	if err != nil {
		return fmt.Errorf("failed to create indexer: %w", err)
	}

	// Set up graceful shutdown
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Channel to listen for interrupt signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Start indexer in a goroutine
	errChan := make(chan error, 1)
	go func() {
		// Determine whether we're using an external DefraDB instance or embedded
		// External DefraDB is used when a URL is configured and Embedded is false
		useExternalDefra := !cfg.DefraDB.Embedded

		if err := chainIndexer.StartIndexing(useExternalDefra); err != nil {
			errChan <- err
		}
	}()

	// Wait for either an error or shutdown signal
	select {
	case err := <-errChan:
		return fmt.Errorf("failed to start indexing: %w", err)
	case sig := <-sigChan:
		fmt.Printf("\nReceived signal %v, shutting down gracefully...\n", sig)
		chainIndexer.StopIndexing()
		cancel()
		fmt.Println("Shutdown complete")
	}

	return nil
}

// findConfigFile returns an explicit or discovered config file, or "" for the built-in default.
func findConfigFile(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if p := os.Getenv("CONFIG_PATH"); p != "" {
		return p
	}
	for _, path := range []string{"./config/config.local.yaml", "./config/config.yaml", "./config.yaml"} {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// printBanner tells the operator where the node is and where its state lives.
func printBanner(cfg *config.Config) {
	port := cfg.Indexer.HealthServerPort
	if port <= 0 {
		port = defaultHTTPPort
	}
	fmt.Printf("\n"+
		"  Shinzo generator %s starting (chain: %s %s, config: %s)\n"+
		"  API + health : http://localhost:%d   (health: /health, GraphQL: /api/v0/graphql)\n"+
		"  Data         : %s\n",
		version, cfg.Chain.Name, cfg.Chain.Network, cfg.Source, port, cfg.DefraDB.Store.Path)
	if cfg.PassphraseGenerated {
		fmt.Printf("  Passphrase   : generated and saved to %s — back it up; it unlocks this node's identity.\n", cfg.PassphraseFile)
	}
	fmt.Printf("  Peer ID      : run `block_poster id` once the node is up\n\n")
}

// healthCmd probes a local node; used by the container HEALTHCHECK and systemd.
func healthCmd(args []string) error {
	fs := flag.NewFlagSet("health", flag.ContinueOnError)
	port := fs.Int("port", defaultHTTPPort, "HTTP port of the local node")
	if err := fs.Parse(args); err != nil {
		return err
	}
	body, code, err := getJSON(fmt.Sprintf("http://127.0.0.1:%d/health", *port))
	if err != nil {
		return err
	}
	status, _ := body["status"].(string)
	fmt.Printf("%s (HTTP %d)\n", status, code)
	if code != http.StatusOK {
		return fmt.Errorf("node reports %q", status)
	}
	return nil
}

// idCmd prints the local node's identity in the forms other people need.
func idCmd(args []string) error {
	fs := flag.NewFlagSet("id", flag.ContinueOnError)
	port := fs.Int("port", defaultHTTPPort, "HTTP port of the local node")
	if err := fs.Parse(args); err != nil {
		return err
	}
	body, _, err := getJSON(fmt.Sprintf("http://127.0.0.1:%d/registration", *port))
	if err != nil {
		return err
	}
	p2p, _ := body["p2p"].(map[string]any)
	self, _ := p2p["self"].(map[string]any)
	reg, _ := body["registration"].(map[string]any)
	fmt.Printf("peer id           : %v\n", self["id"])
	fmt.Printf("connection string : %v\n", reg["connection_string"])
	fmt.Printf("endpoint          : %v\n", reg["endpoint_address"])
	return nil
}

func getJSON(url string) (map[string]any, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), clientTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("no node answering at %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("decode %s: %w", url, err)
	}
	return body, resp.StatusCode, nil
}

// verifySnapshots verifies one or more snapshot files and reports results.
// It returns an error if any snapshot is invalid or verification fails.
func verifySnapshots(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: block_poster verify <snapshot-file.jsonl.gz> [snapshot-file...]")
	}

	allValid := true
	for _, file := range args {
		result, err := snapshot.VerifySnapshot(file)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "FAIL: %s — %v\n", file, err)
			allValid = false
			continue
		}

		if result.Valid {
			_, _ = fmt.Fprintf(stdout, "PASS: %s (blocks %d-%d, %d block sigs, signed by %s)\n",
				file, result.StartBlock, result.EndBlock, result.BlockSigsFound, truncateID(result.SignerIdentity))
		} else {
			_, _ = fmt.Fprintf(stderr, "FAIL: %s — %s\n", file, result.Error)
			allValid = false
		}
	}

	if !allValid {
		return fmt.Errorf("one or more snapshots failed verification")
	}

	return nil
}

func truncateID(id string) string {
	if len(id) <= 20 { //nolint:mnd
		return id
	}
	return id[:20] + "..."
}
