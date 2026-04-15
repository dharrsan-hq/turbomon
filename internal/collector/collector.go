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
	IsAuthoring float64
	IsBacking   float64
	NomCount    float64
	NomStake    float64
	EraPoints   float64
	SessPoints  float64
	BackPoints  float64
	// NEW: Map of ParaID -> Points
	ParaPoints map[string]float64
	Era        string
	Session    string
}

type PolkadotCollector struct {
	cfg    *config.Config
	client *turboflakes.Client
	logger *slog.Logger

	mu             sync.RWMutex
	validatorCache map[string]CachedMetrics
	globalEra      float64
	globalSess     float64

	// Metric Descriptors
	eraNum, sessNum                   *prometheus.Desc
	isAuthoring, isBacking            *prometheus.Desc
	nomCount, nomStake                *prometheus.Desc
	eraPoints, sessPoints, backPoints *prometheus.Desc
	// NEW: Metric Descriptor
	paraBackingPoints *prometheus.Desc
}

func NewPolkadotCollector(cfg *config.Config, logger *slog.Logger) *PolkadotCollector {
	valLabels := []string{"network", "stash", "name", "era", "session"}

	// NEW: Labels specifically for the per-parachain metric
	paraLabels := append(valLabels, "para_id")

	c := &PolkadotCollector{
		cfg:            cfg,
		client:         turboflakes.NewClient(cfg.Host),
		logger:         logger,
		validatorCache: make(map[string]CachedMetrics),

		eraNum:  prometheus.NewDesc("substrate_current_era", "Current Era index", []string{"network"}, nil),
		sessNum: prometheus.NewDesc("substrate_current_session", "Current Session index", []string{"network"}, nil),

		isAuthoring: prometheus.NewDesc("substrate_validator_authoring", "1 if assigned to author blocks this session", valLabels, nil),
		isBacking:   prometheus.NewDesc("substrate_validator_backing", "1 if assigned to parachain backing this session", valLabels, nil),

		nomCount:   prometheus.NewDesc("substrate_nominators_count", "Nominator count", valLabels, nil),
		nomStake:   prometheus.NewDesc("substrate_nominators_stake", "Raw stake in DOT/KSM", valLabels, nil),
		eraPoints:  prometheus.NewDesc("substrate_era_points", "Total era points", valLabels, nil),
		sessPoints: prometheus.NewDesc("substrate_session_points", "Total session points (Auth + Backing)", valLabels, nil),
		backPoints: prometheus.NewDesc("substrate_backing_points", "Total points from parachain backing", valLabels, nil),

		// NEW: Metric Definition
		paraBackingPoints: prometheus.NewDesc("substrate_validator_para_backing_points", "Points earned from backing a specific parachain", paraLabels, nil),
	}

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

	semaphore := make(chan struct{}, c.cfg.ConcurrencyLimit)
	var wg sync.WaitGroup

	for _, v := range c.cfg.Validators {
		wg.Add(1)
		semaphore <- struct{}{}

		go func(val config.ValidatorConfig) {
			defer wg.Done()
			defer func() { <-semaphore }()

			sData, pData, err := c.client.GetValidatorData(val.Address)
			if err != nil {
				c.logger.Warn("scrape failed", "val", val.Name, "err", err)
				return
			}

			authVal, backVal := 0.0, 0.0
			if sData.IsAuth {
				authVal = 1.0
			}
			if sData.IsPara {
				backVal = 1.0
			}

			backingPts := float64(sData.ParaSummary.Pt)
			sessionPts := backingPts + float64(len(sData.Auth.Ab)*20)

			// NEW: Extract Parachain specific points into a new map
			pPoints := make(map[string]float64)
			for paraID, stats := range sData.ParaStats {
				pPoints[paraID] = float64(stats.Pt)
			}

			c.mu.Lock()
			c.validatorCache[val.Address] = CachedMetrics{
				IsAuthoring: authVal,
				IsBacking:   backVal,
				NomCount:    float64(pData.NominatorsCounter),
				NomStake:    pData.NominatorsRawStake / 1e10,
				EraPoints:   float64(sData.Auth.Ep),
				SessPoints:  sessionPts,
				BackPoints:  backingPts,
				ParaPoints:  pPoints, // Store the map in cache
				Era:         eraStr,
				Session:     sessStr,
			}
			c.mu.Unlock()

			time.Sleep(100 * time.Millisecond)
		}(v)
	}
	wg.Wait()
	c.logger.Info("All validators scraped successfully")
}

func (c *PolkadotCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.eraNum
	ch <- c.sessNum
	ch <- c.isAuthoring
	ch <- c.isBacking
	ch <- c.nomCount
	ch <- c.nomStake
	ch <- c.eraPoints
	ch <- c.sessPoints
	ch <- c.backPoints
	ch <- c.paraBackingPoints // Expose new metric descriptor
}

func (c *PolkadotCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	ch <- prometheus.MustNewConstMetric(c.eraNum, prometheus.GaugeValue, c.globalEra, c.cfg.Network)
	ch <- prometheus.MustNewConstMetric(c.sessNum, prometheus.GaugeValue, c.globalSess, c.cfg.Network)

	for _, v := range c.cfg.Validators {
		data, exists := c.validatorCache[v.Address]
		if !exists {
			continue
		}

		baseLabels := []string{c.cfg.Network, v.Address, v.Name, data.Era, data.Session}

		ch <- prometheus.MustNewConstMetric(c.isAuthoring, prometheus.GaugeValue, data.IsAuthoring, baseLabels...)
		ch <- prometheus.MustNewConstMetric(c.isBacking, prometheus.GaugeValue, data.IsBacking, baseLabels...)
		ch <- prometheus.MustNewConstMetric(c.nomCount, prometheus.GaugeValue, data.NomCount, baseLabels...)
		ch <- prometheus.MustNewConstMetric(c.nomStake, prometheus.GaugeValue, data.NomStake, baseLabels...)
		ch <- prometheus.MustNewConstMetric(c.eraPoints, prometheus.GaugeValue, data.EraPoints, baseLabels...)
		ch <- prometheus.MustNewConstMetric(c.sessPoints, prometheus.GaugeValue, data.SessPoints, baseLabels...)
		ch <- prometheus.MustNewConstMetric(c.backPoints, prometheus.GaugeValue, data.BackPoints, baseLabels...)

		// NEW: Loop through the ParaPoints map and emit a metric for EACH parachain
		for paraID, pts := range data.ParaPoints {
			// Append the dynamic paraID to the base labels for this specific metric
			pLabels := append([]string{}, baseLabels...)
			pLabels = append(pLabels, paraID)

			ch <- prometheus.MustNewConstMetric(c.paraBackingPoints, prometheus.GaugeValue, pts, pLabels...)
		}
	}
}
