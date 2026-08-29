package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/shinzonetwork/shinzo-generator-client/pkg/constants"
	"github.com/shinzonetwork/shinzo-generator-client/pkg/pruner"
	"github.com/shinzonetwork/shinzo-generator-client/pkg/snapshot"
)

// CollectionName is the legacy collection name for the shinzo network.
const CollectionName = "shinzo"

// DefraDBP2PConfig represents P2P configuration for DefraDB.
type DefraDBP2PConfig struct {
	BootstrapPeers      []string `yaml:"bootstrap_peers"`
	ListenAddr          string   `yaml:"listen_addr"`
	Enabled             bool     `yaml:"enabled"`
	AcceptIncoming      bool     `yaml:"accept_incoming"`
	MaxRetries          int      `yaml:"max_retries"`
	RetryBaseDelayMs    int      `yaml:"retry_base_delay_ms"`
	ReconnectIntervalMs int      `yaml:"reconnect_interval_ms"`
	EnableAutoReconnect bool     `yaml:"enable_auto_reconnect"`
	// AnnounceAddr is the P2P address other nodes should dial when it differs
	// from where we listen (NAT, port remap, DNS name). It is what /registration
	// advertises. Overridable with P2P_ANNOUNCE_ADDR.
	AnnounceAddr string `yaml:"announce_addr"`
}

// DefraDBStoreConfig represents store configuration for DefraDB.
type DefraDBStoreConfig struct {
	Path string `yaml:"path"`
	// Badger memory configuration
	BlockCacheMB int64 `yaml:"block_cache_mb"`
	MemTableMB   int64 `yaml:"memtable_mb"`
	IndexCacheMB int64 `yaml:"index_cache_mb"`
	// Badger compaction configuration
	NumCompactors           int `yaml:"num_compactors"`
	NumLevelZeroTables      int `yaml:"num_level_zero_tables"`
	NumLevelZeroTablesStall int `yaml:"num_level_zero_tables_stall"`
	// Badger value log configuration
	ValueLogFileSizeMB int64 `yaml:"value_log_file_size_mb"` // Size of each vlog file (default 64MB).
}

// DefraDBConfig represents DefraDB configuration.
type DefraDBConfig struct {
	URL           string             `yaml:"url"`
	KeyringSecret string             `yaml:"keyring_secret"`
	Embedded      bool               `yaml:"embedded"`
	P2P           DefraDBP2PConfig   `yaml:"p2p"`
	Store         DefraDBStoreConfig `yaml:"store"`
}

// Host returns the DefraDB host URL for backward compatibility.
func (d *DefraDBConfig) Host() string {
	return d.URL
}

// ChainConfig represents the EVM chain being indexed.
type ChainConfig struct {
	Name    string `yaml:"name"`    // e.g. "Ethereum", "Arbitrum", "Optimism", "Avalanche"
	Network string `yaml:"network"` // e.g. "Mainnet", "Testnet"
	Hub     string `yaml:"hub"`     // ShinzoHub hostname only — no scheme, no port (e.g. "testnet.shinzo.network")
}

// GethConfig represents Geth node configuration.
type GethConfig struct {
	NodeURL    string `yaml:"node_url"`
	WsURL      string `yaml:"ws_url"`
	APIKey     string `yaml:"api_key"`
	APIKeyType string `yaml:"api_key_type"`
}

// IndexerConfig represents indexer configuration.
type IndexerConfig struct {
	StartHeight        int  `yaml:"start_height"`
	ConcurrentBlocks   int  `yaml:"concurrent_blocks"`
	ReceiptWorkers     int  `yaml:"receipt_workers"`
	MaxDocsPerTxn      int  `yaml:"max_docs_per_txn"`
	MaxTxDocsPerBatch  int  `yaml:"max_tx_docs_per_batch"`
	MaxLogDocsPerBatch int  `yaml:"max_log_docs_per_batch"`
	MaxALEDocsPerBatch int  `yaml:"max_ale_docs_per_batch"`
	BlocksPerMinute    int  `yaml:"blocks_per_minute"`
	HealthServerPort   int  `yaml:"health_server_port"`
	OpenBrowserOnStart bool `yaml:"open_browser_on_start"`
	// HTTP configures the public HTTP surface (CORS, TLS) of the health server.
	HTTP           HTTPConfig `yaml:"http"`
	StartBuffer    int        `yaml:"start_buffer"`
	SchemaAuthMode string     `yaml:"schema_auth_mode"`
	// SchemaAPIKeys are the accepted bearer tokens for the /api/v1/schema/* endpoints.
	//
	// ⚠ IMPORTANT: This field uses yaml:"-", which means YAML configuration is SILENTLY IGNORED.
	// Keys MUST be provided via the SCHEMA_API_KEYS environment variable as a comma-separated list.
	// Setting this field in config.yaml will NOT work — the server will start with zero keys,
	// causing ALL schema requests to return 503 Service Unavailable (fail-closed auth).
	SchemaAPIKeys []string `yaml:"-"`
}

// HTTPConfig configures the node's public HTTP surface (the health server).
type HTTPConfig struct {
	// AllowedOrigins lists browser origins permitted to call this node
	// (e.g. "https://explorer.shinzo.network"). "*" allows any origin.
	// Empty disables CORS entirely.
	AllowedOrigins []string `yaml:"allowed_origins"`
	// TLS, when both files are set, serves HTTPS directly from the node.
	TLS TLSConfig `yaml:"tls"`
	// TrustedProxies are the CIDRs (or IPs) of reverse proxies whose
	// X-Forwarded-Host/Proto headers are honoured when building the node's
	// advertised endpoint. Empty (default) = ignore those headers entirely.
	// Overridable with TRUSTED_PROXIES (comma-separated).
	TrustedProxies []string `yaml:"trusted_proxies"`
}

// TLSConfig points at a PEM certificate/key pair.
type TLSConfig struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// LoggerConfig represents logger configuration.
type LoggerConfig struct {
	Development bool `yaml:"development"`
}

// Config represents the main configuration structure.
type Config struct {
	Chain    ChainConfig     `yaml:"chain"`
	DefraDB  DefraDBConfig   `yaml:"defradb"`
	Geth     GethConfig      `yaml:"geth"`
	Indexer  IndexerConfig   `yaml:"indexer"`
	Pruner   pruner.Config   `yaml:"pruner"`
	Snapshot snapshot.Config `yaml:"snapshot"`
	Logger   LoggerConfig    `yaml:"logger"`

	// Resolved at load time, not from YAML.
	Source              string `yaml:"-"` // file path or "<built-in default>"
	PassphraseFile      string `yaml:"-"` // where the passphrase was read from / written to, if a file
	PassphraseGenerated bool   `yaml:"-"` // true on the run that created PassphraseFile
}

// LoadConfig loads configuration from a YAML file. See Load for the
// no-file (compiled-in default) path.
func LoadConfig(path string) (*Config, error) {
	return Load(path)
}

// applyDefaults sets default values for optional configuration.
func applyDefaults(cfg *Config) {
	if cfg.Chain.Name == "" {
		cfg.Chain.Name = "Ethereum"
	}
	if cfg.Chain.Network == "" {
		cfg.Chain.Network = "Mainnet"
	}
	if cfg.Indexer.ConcurrentBlocks <= 0 {
		cfg.Indexer.ConcurrentBlocks = 8
	}
	if cfg.Indexer.ReceiptWorkers <= 0 {
		cfg.Indexer.ReceiptWorkers = 16
	}
	if cfg.Indexer.MaxDocsPerTxn <= 0 {
		cfg.Indexer.MaxDocsPerTxn = 1000
	}
	// Per-collection batch sizes default to 0, meaning "use MaxDocsPerTxn".
	if cfg.Indexer.HealthServerPort == 0 {
		cfg.Indexer.HealthServerPort = 8080
	}
	if cfg.Indexer.StartBuffer <= 0 {
		cfg.Indexer.StartBuffer = 100
	}
	if cfg.Indexer.SchemaAuthMode == "" {
		cfg.Indexer.SchemaAuthMode = constants.SchemaAuthModeToken
	}

	// Pruner defaults.
	cfg.Pruner.SetDefaults()

	// Snapshot defaults.
	cfg.Snapshot.SetDefaults()
}

// validateConfig validates the configuration.
func validateConfig(cfg *Config) error {
	if cfg.Indexer.StartHeight < 0 {
		return fmt.Errorf("start_height must be >= 0")
	}

	switch cfg.Indexer.SchemaAuthMode {
	case constants.SchemaAuthModeNone, constants.SchemaAuthModeToken, constants.SchemaAuthModeMTLS:
	default:
		return fmt.Errorf("invalid SCHEMA_AUTH_MODE %q: must be one of none, token, mtls", cfg.Indexer.SchemaAuthMode)
	}

	// When using an external DefraDB instance (embedded=false), a URL is required.
	// Embedded DefraDB can run on a random port when URL is empty.
	if !cfg.DefraDB.Embedded && strings.TrimSpace(cfg.DefraDB.URL) == "" {
		return fmt.Errorf("external DefraDB requires a non-empty url")
	}
	return nil
}

// applyEnvOverrides applies environment variable overrides to the config.
func applyEnvOverrides(cfg *Config) error {
	if err := applyDefraEnvOverrides(cfg); err != nil {
		return err
	}
	applyChainEnvOverrides(cfg)
	applyIndexerEnvOverrides(cfg)
	applySchemaEnvOverrides(cfg)
	applyPrunerEnvOverrides(cfg)
	applySnapshotEnvOverrides(cfg)

	if loggerDebug := os.Getenv("LOGGER_DEBUG"); loggerDebug != "" {
		if debug, err := strconv.ParseBool(loggerDebug); err == nil {
			cfg.Logger.Development = debug
		}
	}
	return nil
}

// applyDefraEnvOverrides applies DefraDB-related environment variable overrides.
func applyDefraEnvOverrides(cfg *Config) error {
	if defraURL := os.Getenv("DEFRADB_URL"); defraURL != "" {
		cfg.DefraDB.URL = defraURL
	} else if host := os.Getenv("DEFRADB_HOST"); host != "" {
		if port := os.Getenv("DEFRADB_PORT"); port != "" {
			cfg.DefraDB.URL = fmt.Sprintf("http://%s:%s", host, port)
		} else {
			cfg.DefraDB.URL = fmt.Sprintf("http://%s:9181", host)
		}
	}

	secret, err := keyringSecretFromEnv()
	if err != nil {
		return err
	}
	if secret != "" {
		cfg.DefraDB.KeyringSecret = secret
	}
	if p2pEnabled := os.Getenv("DEFRADB_P2P_ENABLED"); p2pEnabled != "" {
		if parsed, err := strconv.ParseBool(p2pEnabled); err == nil {
			cfg.DefraDB.P2P.Enabled = parsed
		}
	}
	if listenAddr := os.Getenv("DEFRADB_P2P_LISTEN_ADDR"); listenAddr != "" {
		cfg.DefraDB.P2P.ListenAddr = listenAddr
	}
	applyNetworkEnvOverrides(cfg)
	if acceptIncoming := os.Getenv("DEFRADB_P2P_ACCEPT_INCOMING"); acceptIncoming != "" {
		if parsed, err := strconv.ParseBool(acceptIncoming); err == nil {
			cfg.DefraDB.P2P.AcceptIncoming = parsed
		}
	}
	if storePath := os.Getenv("DEFRADB_STORE_PATH"); storePath != "" {
		cfg.DefraDB.Store.Path = storePath
	}
	if blockCacheMB := os.Getenv("DEFRADB_BLOCK_CACHE_MB"); blockCacheMB != "" {
		if n, err := strconv.ParseInt(blockCacheMB, 10, 64); err == nil {
			cfg.DefraDB.Store.BlockCacheMB = n
		}
	}
	if memtableMB := os.Getenv("DEFRADB_MEMTABLE_MB"); memtableMB != "" {
		if n, err := strconv.ParseInt(memtableMB, 10, 64); err == nil {
			cfg.DefraDB.Store.MemTableMB = n
		}
	}
	if indexCacheMB := os.Getenv("DEFRADB_INDEX_CACHE_MB"); indexCacheMB != "" {
		if n, err := strconv.ParseInt(indexCacheMB, 10, 64); err == nil {
			cfg.DefraDB.Store.IndexCacheMB = n
		}
	}
	if numCompactors := os.Getenv("DEFRADB_NUM_COMPACTORS"); numCompactors != "" {
		if n, err := strconv.Atoi(numCompactors); err == nil {
			cfg.DefraDB.Store.NumCompactors = n
		}
	}
	if numL0Tables := os.Getenv("DEFRADB_NUM_LEVEL_ZERO_TABLES"); numL0Tables != "" {
		if n, err := strconv.Atoi(numL0Tables); err == nil {
			cfg.DefraDB.Store.NumLevelZeroTables = n
		}
	}
	if numL0TablesStall := os.Getenv("DEFRADB_NUM_LEVEL_ZERO_TABLES_STALL"); numL0TablesStall != "" {
		if n, err := strconv.Atoi(numL0TablesStall); err == nil {
			cfg.DefraDB.Store.NumLevelZeroTablesStall = n
		}
	}
	return nil
}

// applyChainEnvOverrides applies chain and Geth environment variable overrides.
func applyChainEnvOverrides(cfg *Config) {
	if chainName := os.Getenv("CHAIN_NAME"); chainName != "" {
		cfg.Chain.Name = chainName
	}
	if chainNetwork := os.Getenv("CHAIN_NETWORK"); chainNetwork != "" {
		cfg.Chain.Network = chainNetwork
	}
	if shinzoHubHost := os.Getenv("SHINZOHUB_REST_BASE"); shinzoHubHost != "" {
		cfg.Chain.Hub = shinzoHubHost
	}
	if gethRPCURL := os.Getenv("GETH_RPC_URL"); gethRPCURL != "" {
		cfg.Geth.NodeURL = gethRPCURL
	}
	if gethWsURL := os.Getenv("GETH_WS_URL"); gethWsURL != "" {
		cfg.Geth.WsURL = gethWsURL
	}
	if gethAPIKey := os.Getenv("GETH_API_KEY"); gethAPIKey != "" {
		cfg.Geth.APIKey = gethAPIKey
	}
	if gethAPIKeyType := os.Getenv("GETH_API_KEY_TYPE"); gethAPIKeyType != "" {
		cfg.Geth.APIKeyType = gethAPIKeyType
	}
}

// applyIndexerEnvOverrides applies indexer environment variable overrides.
func applyIndexerEnvOverrides(cfg *Config) {
	if startHeight := os.Getenv("INDEXER_START_HEIGHT"); startHeight != "" {
		if h, err := strconv.Atoi(startHeight); err == nil {
			cfg.Indexer.StartHeight = h
		}
	}
	if concurrentBlocks := os.Getenv("INDEXER_CONCURRENT_BLOCKS"); concurrentBlocks != "" {
		if n, err := strconv.Atoi(concurrentBlocks); err == nil {
			cfg.Indexer.ConcurrentBlocks = n
		}
	}
	if receiptWorkers := os.Getenv("INDEXER_RECEIPT_WORKERS"); receiptWorkers != "" {
		if n, err := strconv.Atoi(receiptWorkers); err == nil {
			cfg.Indexer.ReceiptWorkers = n
		}
	}
	if maxDocsPerTxn := os.Getenv("INDEXER_MAX_DOCS_PER_TXN"); maxDocsPerTxn != "" {
		if n, err := strconv.Atoi(maxDocsPerTxn); err == nil {
			cfg.Indexer.MaxDocsPerTxn = n
		}
	}
	if v := os.Getenv("INDEXER_MAX_TX_DOCS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Indexer.MaxTxDocsPerBatch = n
		}
	}
	if v := os.Getenv("INDEXER_MAX_LOG_DOCS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Indexer.MaxLogDocsPerBatch = n
		}
	}
	if v := os.Getenv("INDEXER_MAX_ALE_DOCS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Indexer.MaxALEDocsPerBatch = n
		}
	}
	if blocksPerMinute := os.Getenv("INDEXER_BLOCKS_PER_MINUTE"); blocksPerMinute != "" {
		if n, err := strconv.Atoi(blocksPerMinute); err == nil {
			cfg.Indexer.BlocksPerMinute = n
		}
	}
	if healthPort := os.Getenv("INDEXER_HEALTH_SERVER_PORT"); healthPort != "" {
		if n, err := strconv.Atoi(healthPort); err == nil {
			cfg.Indexer.HealthServerPort = n
		}
	}
	if startBuffer := os.Getenv("INDEXER_START_BUFFER"); startBuffer != "" {
		if n, err := strconv.Atoi(startBuffer); err == nil {
			cfg.Indexer.StartBuffer = n
		}
	}
}

func applySchemaEnvOverrides(cfg *Config) {
	if mode := os.Getenv("SCHEMA_AUTH_MODE"); mode != "" {
		cfg.Indexer.SchemaAuthMode = mode
	}
	if keys := os.Getenv("SCHEMA_API_KEYS"); keys != "" {
		raw := strings.Split(keys, ",")
		trimmed := make([]string, 0, len(raw))
		for _, k := range raw {
			k = strings.TrimSpace(k)
			if k != "" {
				trimmed = append(trimmed, k)
			}
		}
		cfg.Indexer.SchemaAPIKeys = trimmed
	}
}

// applyPrunerEnvOverrides applies pruner environment variable overrides.
func applyPrunerEnvOverrides(cfg *Config) {
	if prunerEnabled := os.Getenv("PRUNER_ENABLED"); prunerEnabled != "" {
		if enabled, err := strconv.ParseBool(prunerEnabled); err == nil {
			cfg.Pruner.Enabled = enabled
		}
	}
	if prunerMaxBlocks := os.Getenv("PRUNER_MAX_BLOCKS"); prunerMaxBlocks != "" {
		if n, err := strconv.ParseInt(prunerMaxBlocks, 10, 64); err == nil {
			cfg.Pruner.MaxBlocks = n
		}
	}
	if prunerThreshold := os.Getenv("PRUNER_PRUNE_THRESHOLD"); prunerThreshold != "" {
		if n, err := strconv.ParseInt(prunerThreshold, 10, 64); err == nil {
			cfg.Pruner.PruneThreshold = n
		}
	}
	if prunerInterval := os.Getenv("PRUNER_INTERVAL_SECONDS"); prunerInterval != "" {
		if n, err := strconv.Atoi(prunerInterval); err == nil {
			cfg.Pruner.IntervalSeconds = n
		}
	}
}

// applySnapshotEnvOverrides applies snapshot environment variable overrides.
func applySnapshotEnvOverrides(cfg *Config) {
	if snapshotEnabled := os.Getenv("SNAPSHOT_ENABLED"); snapshotEnabled != "" {
		if enabled, err := strconv.ParseBool(snapshotEnabled); err == nil {
			cfg.Snapshot.Enabled = enabled
		}
	}
	if snapshotDir := os.Getenv("SNAPSHOT_DIR"); snapshotDir != "" {
		cfg.Snapshot.Dir = snapshotDir
	}
	if blocksPerFile := os.Getenv("SNAPSHOT_BLOCKS_PER_FILE"); blocksPerFile != "" {
		if n, err := strconv.ParseInt(blocksPerFile, 10, 64); err == nil {
			cfg.Snapshot.BlocksPerFile = n
		}
	}
	if snapshotInterval := os.Getenv("SNAPSHOT_INTERVAL_SECONDS"); snapshotInterval != "" {
		if n, err := strconv.Atoi(snapshotInterval); err == nil {
			cfg.Snapshot.IntervalSeconds = n
		}
	}
}

// keyringSecretFromEnv returns the passphrase that encrypts the node's identity
// keys: SHINZO_KEY_PASSPHRASE (or the legacy DEFRADB_KEYRING_SECRET /
// DEFRA_KEYRING_SECRET), else the contents of SHINZO_KEY_PASSPHRASE_FILE
// (Docker/Kubernetes secrets are mounted as files). Empty when none is set.
func keyringSecretFromEnv() (string, error) {
	for _, name := range []string{"SHINZO_KEY_PASSPHRASE", "DEFRADB_KEYRING_SECRET", "DEFRA_KEYRING_SECRET"} {
		if v := os.Getenv(name); v != "" {
			return v, nil
		}
	}
	f := os.Getenv("SHINZO_KEY_PASSPHRASE_FILE")
	if f == "" {
		return "", nil
	}
	b, err := os.ReadFile(filepath.Clean(f))
	if err != nil {
		return "", fmt.Errorf("read SHINZO_KEY_PASSPHRASE_FILE: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// applyNetworkEnvOverrides covers how the node is reached: browser origins,
// trusted reverse proxies, and the P2P address to advertise.
func applyNetworkEnvOverrides(cfg *Config) {
	if origins := os.Getenv("ALLOWED_ORIGINS"); origins != "" { // comma-separated browser origins
		cfg.Indexer.HTTP.AllowedOrigins = strings.Split(origins, ",")
	}
	if v := os.Getenv("TRUSTED_PROXIES"); v != "" {
		cfg.Indexer.HTTP.TrustedProxies = strings.Split(v, ",")
	}
	if announce := os.Getenv("P2P_ANNOUNCE_ADDR"); announce != "" {
		cfg.DefraDB.P2P.AnnounceAddr = announce
	}
}
