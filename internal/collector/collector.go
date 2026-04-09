package collector

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/dharrsan-hq/turbomon/internal/config"
	"github.com/dharrsan-hq/turbomon/internal/turboflakes"
	"github.com/prometheus/client_golang/prometheus"
)

// CachedMetrics holds the latest data for a validator
type CachedMetrics struct {
	Status     float64
	NomCount   float64
	NomStake   float64
	EraPoints  float64
	SessPoints float64
	BackPoints float64
	Era        string
	Session    string
}

type PolkadotCollector struct {
	cfg    *config.Config
	client *turboflakes.Client
	logger *slog.Logger

	// Cache state
	mu             sync.RWMutex
	validatorCache map[string]CachedMetrics
	globalEra      float64
	globalSess     float64

	// Metric Descriptors
	eraNum, sessNum, status, nomCount, nomStake, eraPoints, sessPoints, backPoints *prometheus.Desc
}

func NewPolkadotCollector(cfg *config.Config, logger *slog.Logger) *PolkadotCollector {
	valLabels := []string{"network", "stash", "name", "era", "session"}

	c := &PolkadotCollector{
		cfg:            cfg,
		client:         turboflakes.NewClient(cfg.Host),
		logger:         logger,
		validatorCache: make(map[string]CachedMetrics),

		eraNum:     prometheus.NewDesc("substrate_current_era", "Current Era", []string{"network"}, nil),
		sessNum:    prometheus.NewDesc("substrate_current_session", "Current Session", []string{"network"}, nil),
		status:     prometheus.NewDesc("substrate_validator_status", "1=Active, 0=Waiting", valLabels, nil),
		nomCount:   prometheus.NewDesc("substrate_nominators_count", "Nominator count", valLabels, nil),
		nomStake:   prometheus.NewDesc("substrate_nominators_stake", "Raw stake in DOT", valLabels, nil),
		eraPoints:  prometheus.NewDesc("substrate_era_points", "Total era points", valLabels, nil),
		sessPoints: prometheus.NewDesc("substrate_session_points", "Total session points", valLabels, nil),
		backPoints: prometheus.NewDesc("substrate_backing_points", "Parachain backing points", valLabels, nil),
	}

	// Start the background scraper immediately
	go c.startBackgroundScraper()
	return c
}

func (c *PolkadotCollector) startBackgroundScraper() {
	ticker := time.NewTicker(c.cfg.ScrapeInterval)
	c.logger.Info("Background scraper started", "interval", c.cfg.ScrapeInterval, "concurrency", c.cfg.ConcurrencyLimit)

	for {
		c.scrapeAll()
		<-ticker.C
	}
}

func (c *PolkadotCollector) scrapeAll() {
	gs, err := c.client.GetGlobalSession()
	if err != nil {
		c.logger.Error("failed to fetch global session", "error", err)
		return
	}

	c.mu.Lock()
	c.globalEra = float64(gs.Eix)
	c.globalSess = float64(gs.Six)
	c.mu.Unlock()

	eraStr := fmt.Sprintf("%d", gs.Eix)
	sessStr := fmt.Sprintf("%d", gs.Six)

	// Concurrency control using a Semaphore
	semaphore := make(chan struct{}, c.cfg.ConcurrencyLimit)
	var wg sync.WaitGroup

	for _, v := range c.cfg.Validators {
		wg.Add(1)
		semaphore <- struct{}{} // Acquire

		go func(val config.ValidatorConfig) {
			defer wg.Done()
			defer func() { <-semaphore }() // Release

			sData, pData, err := c.client.GetValidatorData(val.Address)
			if err != nil {
				c.logger.Warn("scrape failed", "val", val.Name, "err", err)
				return
			}

			status := 0.0
			if sData.IsAuth || sData.IsPara {
				status = 1.0
			}

			backing := float64(sData.ParaSummary.Pt)
			sessionPts := backing + float64(len(sData.Auth.Ab)*20)

			// Update cache
			c.mu.Lock()
			c.validatorCache[val.Address] = CachedMetrics{
				Status:     status,
				NomCount:   float64(pData.NominatorsCounter),
				NomStake:   pData.NominatorsRawStake / 1e10,
				EraPoints:  float64(sData.Auth.Ep),
				SessPoints: sessionPts,
				BackPoints: backing,
				Era:        eraStr,
				Session:    sessStr,
			}
			c.mu.Unlock()

			// Optional: Small sleep to stagger requests even within the concurrency limit
			time.Sleep(100 * time.Millisecond)
		}(v)
	}
	wg.Wait()
	c.logger.Info("All validators scraped successfully")
}

func (c *PolkadotCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.eraNum
	ch <- c.sessNum
	ch <- c.status
	ch <- c.nomCount
	ch <- c.nomStake
	ch <- c.eraPoints
	ch <- c.sessPoints
	ch <- c.backPoints
}

func (c *PolkadotCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Global Metrics
	ch <- prometheus.MustNewConstMetric(c.eraNum, prometheus.GaugeValue, c.globalEra, c.cfg.Network)
	ch <- prometheus.MustNewConstMetric(c.sessNum, prometheus.GaugeValue, c.globalSess, c.cfg.Network)

	// Validator Metrics from Cache
	for _, v := range c.cfg.Validators {
		data, exists := c.validatorCache[v.Address]
		if !exists {
			continue
		}

		labels := []string{c.cfg.Network, v.Address, v.Name, data.Era, data.Session}
		ch <- prometheus.MustNewConstMetric(c.status, prometheus.GaugeValue, data.Status, labels...)
		ch <- prometheus.MustNewConstMetric(c.nomCount, prometheus.GaugeValue, data.NomCount, labels...)
		ch <- prometheus.MustNewConstMetric(c.nomStake, prometheus.GaugeValue, data.NomStake, labels...)
		ch <- prometheus.MustNewConstMetric(c.eraPoints, prometheus.GaugeValue, data.EraPoints, labels...)
		ch <- prometheus.MustNewConstMetric(c.sessPoints, prometheus.GaugeValue, data.SessPoints, labels...)
		ch <- prometheus.MustNewConstMetric(c.backPoints, prometheus.GaugeValue, data.BackPoints, labels...)
	}
}
