package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"

	"github.com/dharrsan-hq/turbomon/internal/collector"
	"github.com/dharrsan-hq/turbomon/internal/config"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	configPath := flag.String("config", "polkadot-stash.yaml", "Path to config")
	port := flag.String("port", "9101", "Metrics port")
	flag.Parse()

	// Initialize Structured Logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	reg := prometheus.NewRegistry()
	coll := collector.NewPolkadotCollector(cfg, logger)
	reg.MustRegister(coll)

	http.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))

	logger.Info("Starting TurboMon Exporter",
		"network", cfg.Network,
		"port", *port,
		"validators", len(cfg.Validators),
	)

	if err := http.ListenAndServe(":"+*port, nil); err != nil {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}
