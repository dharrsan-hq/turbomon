# TurboMon

TurboMon is a production-grade Prometheus exporter for Polkadot and Kusama validators. It leverages the TurboFlakes API to provide deep insights into validator performance, including session points, backing points, and nominator distribution.

Unlike standard exporters, TurboMon is architected with a **Background-Cache Pattern** to support large validator sets (50+) without triggering API rate limits or causing Prometheus scrape timeouts.

## 🏗 Architecture

TurboMon decouples the Metric Scraping from Metric Serving:

1. **Background Scraper:** A dedicated goroutine polls the TurboFlakes API at a configurable interval.
2. **Concurrency Control:** Uses a semaphore pattern to ensure only a fixed number of API requests are in-flight simultaneously.
3. **In-Memory Cache:** Latest results are stored in a thread-safe cache.
4. **Prometheus Handler:** Serves metrics instantly from memory when scraped, ensuring sub-millisecond response times to your monitoring stack.

## 📊 Exported Metrics

All metrics include labels for `network`, `stash`, `name`, `era`, and `session`.

| Metric Name | Type | Description |
|---|---|---|
| `substrate_current_era` | Gauge | The current era index of the network. |
| `substrate_current_session` | Gauge | The current session index of the network. |
| `substrate_validator_status` | Gauge | 1 if active (authoring or backing), 0 otherwise. |
| `substrate_nominators_count` | Gauge | Total number of nominators for the stash. |
| `substrate_nominators_stake` | Gauge | Total raw DOT/KSM stake (unweighted). |
| `substrate_era_points` | Gauge | Total points accumulated in the current era. |
| `substrate_session_points` | Gauge | Calculated points for the current session (Backing + Block Auth). |
| `substrate_backing_points` | Gauge | Points earned specifically from parachain backing. |

## 🚀 Getting Started


### Installation

```bash
go mod tidy
go build -o turbomon ./cmd/exporter
```

### Configuration

Create a YAML file (e.g., `polkadot-stash.yaml`):

```yaml
network: polkadot
turboflakes_api_host: polkadot-onet-api.turboflakes.io
scrape_interval: 120s      # API polling frequency
concurrency_limit: 3       # Max simultaneous API calls
validators:
  - name: my-val-1
    address: 1vTaLKEyj2Wn9xEkUGixBkVXJAd4pzDgXzz9CuVjhVqhHRQ
  - name: my-val-2
    address: 14Tx55srzt7iCKJFpXhjfFsKJ72ZKc1oAWQiLxhpEGWgPKkt
```

### Running

```bash
./turbomon -config=polkadot-stash.yaml -port=9101
```