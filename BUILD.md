# Build from source

## Prerequisites

- Go 1.26+
- Make
- A blockchain node with JSON-RPC and WebSocket access.

## Steps

```shell
git clone git@github.com:shinzonetwork/shinzo-generator-client.git
cd shinzo-generator-client
cp .env.sample .env   # fill in your node credentials
go mod download
make build
```

The compiled binary goes into `./bin`.

## Useful commands

| Command | What it does |
| --- | --- |
| `make build` | Build the generator binary (standard mode). |
| `make start` | Run the compiled binary. |
| `make test` | Run all tests with a summary. |
| `make integration-test` | Run mock and live integration tests. |
| `make coverage` | Generate an HTML coverage report. |
| `make geth-status` | Only applies to Ethereum Geth nodes. Check Geth node connectivity and current block number. |
| `make clean` | Remove build artifacts. |
| `make stop` | Stop running generator and DefraDB processes. |
| `make help` | Show all available targets. |

## Ports

| Port | Service |
| --- | --- |
| `9181` | DefraDB GraphQL + REST API (direct) |
| `9171` | libp2p P2P networking |
| `8080` | Health, metrics, registration, snapshots, schema, and a reverse proxy to the DefraDB API under `/api/v0/` |

`docker-compose.yml` publishes the HTTP server on host port `80` (container `8080`) and P2P on `9171`; `9181` stays internal to the container. The HTTP port is the only one a browser client or dashboard needs. CORS origins and optional TLS for it are set under `indexer.http` in `config.yaml`; put your own reverse proxy in front if you'd rather terminate TLS there.
