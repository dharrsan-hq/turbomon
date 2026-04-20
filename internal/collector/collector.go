package collector

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/dharrsan-hq/turbomon/internal/config"
	"github.com/dharrsan-hq/turbomon/internal/turboflakes"
	"github.com/prometheus/client_golang/prometheus"

	gsrpc "github.com/centrifuge/go-substrate-rpc-client/v4"
	"github.com/centrifuge/go-substrate-rpc-client/v4/types"
	"github.com/vedhavyas/go-subkey/v2"
)

// CachedMetrics holds the latest data for a validator
type CachedMetrics struct {
	IsActive    float64 // NEW: 1.0 for Active, 0.0 for Waiting
	IsAuthoring float64
	IsBacking   float64
	NomCount    float64
	NomStake    float64
	EraPoints   float64
	SessPoints  float64
	BackPoints  float64
	ParaPoints  map[string]float64
	Era         string
	Session     string
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
	validatorStatus                   *prometheus.Desc // NEW: Metric Descriptor
	isAuthoring, isBacking            *prometheus.Desc
	nomCount, nomStake                *prometheus.Desc
	eraPoints, sessPoints, backPoints *prometheus.Desc
	paraBackingPoints                 *prometheus.Desc
}

func NewPolkadotCollector(cfg *config.Config, logger *slog.Logger) *PolkadotCollector {
	valLabels := []string{"network", "stash", "name", "era", "session"}
	paraLabels := append(valLabels, "para_id")

	c := &PolkadotCollector{
		cfg:            cfg,
		client:         turboflakes.NewClient(cfg.Host),
		logger:         logger,
		validatorCache: make(map[string]CachedMetrics),

		eraNum:  prometheus.NewDesc("substrate_current_era", "Current Era index", []string{"network"}, nil),
		sessNum: prometheus.NewDesc("substrate_current_session", "Current Session index", []string{"network"}, nil),

		// NEW: Metric Definition
		validatorStatus: prometheus.NewDesc("substrate_validator_status", "1 if active in current session, 0 otherwise", valLabels, nil),

		isAuthoring:       prometheus.NewDesc("substrate_validator_authoring", "1 if assigned to author blocks this session", valLabels, nil),
		isBacking:         prometheus.NewDesc("substrate_validator_backing", "1 if assigned to parachain backing this session", valLabels, nil),
		nomCount:          prometheus.NewDesc("substrate_nominators_count", "Nominator count", valLabels, nil),
		nomStake:          prometheus.NewDesc("substrate_nominators_stake", "Raw stake in DOT/KSM", valLabels, nil),
		eraPoints:         prometheus.NewDesc("substrate_era_points", "Total era points", valLabels, nil),
		sessPoints:        prometheus.NewDesc("substrate_session_points", "Total session points (Auth + Backing)", valLabels, nil),
		backPoints:        prometheus.NewDesc("substrate_backing_points", "Total points from parachain backing", valLabels, nil),
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

// NEW: Helper function to pull the active set from the chain
func (c *PolkadotCollector) getActiveSet() (map[string]bool, error) {
	if c.cfg.RPCURL == "" {
		return nil, fmt.Errorf("RPC URL is not configured")
	}

	api, err := gsrpc.NewSubstrateAPI(c.cfg.RPCURL)
	if err != nil {
		return nil, err
	}

	meta, err := api.RPC.State.GetMetadataLatest()
	if err != nil {
		return nil, err
	}

	key, err := types.CreateStorageKey(meta, "Session", "Validators", nil)
	if err != nil {
		return nil, err
	}

	var activeAccountIDs []types.AccountID
	_, err = api.RPC.State.GetStorageLatest(key, &activeAccountIDs)
	if err != nil {
		return nil, err
	}

	// Determine SS58 Prefix based on network
	var prefix uint16 = 42
	networkLower := strings.ToLower(c.cfg.Network)
	if strings.Contains(networkLower, "polkadot") {
		prefix = 0
	} else if strings.Contains(networkLower, "kusama") {
		prefix = 2
	}

	activeMap := make(map[string]bool)
	for _, accID := range activeAccountIDs {
		address := subkey.SS58Encode(accID[:], prefix)
		activeMap[address] = true
	}
	return activeMap, nil
}

func (c *PolkadotCollector) scrapeAll() {
	// 1. Fetch Active Set FIRST (Fail silently and continue if RPC is down)
	activeSet, err := c.getActiveSet()
	if err != nil {
		c.logger.Warn("failed to fetch active set from RPC; retaining last cached status for all validators", "error", err)
	}

	// 2. Fetch Global Session Info
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

	// 3. Loop over validators
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

			isActive := 0.0
			if activeSet != nil {
				if activeSet[val.Address] {
					isActive = 1.0
				}
			} else {
				c.mu.RLock()
				if prev, ok := c.validatorCache[val.Address]; ok {
					isActive = prev.IsActive
				}
				c.mu.RUnlock()
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

			pPoints := make(map[string]float64)
			for paraID, stats := range sData.ParaStats {
				pPoints[paraID] = float64(stats.Pt)
			}

			c.mu.Lock()
			c.validatorCache[val.Address] = CachedMetrics{
				IsActive:    isActive, // NEW: Cached
				IsAuthoring: authVal,
				IsBacking:   backVal,
				NomCount:    float64(pData.NominatorsCounter),
				NomStake:    pData.NominatorsRawStake / 1e10,
				EraPoints:   float64(sData.Auth.Ep),
				SessPoints:  sessionPts,
				BackPoints:  backingPts,
				ParaPoints:  pPoints,
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
	ch <- c.validatorStatus // NEW
	ch <- c.isAuthoring
	ch <- c.isBacking
	ch <- c.nomCount
	ch <- c.nomStake
	ch <- c.eraPoints
	ch <- c.sessPoints
	ch <- c.backPoints
	ch <- c.paraBackingPoints
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

		// NEW: Emit metric
		ch <- prometheus.MustNewConstMetric(c.validatorStatus, prometheus.GaugeValue, data.IsActive, baseLabels...)

		ch <- prometheus.MustNewConstMetric(c.isAuthoring, prometheus.GaugeValue, data.IsAuthoring, baseLabels...)
		ch <- prometheus.MustNewConstMetric(c.isBacking, prometheus.GaugeValue, data.IsBacking, baseLabels...)
		ch <- prometheus.MustNewConstMetric(c.nomCount, prometheus.GaugeValue, data.NomCount, baseLabels...)
		ch <- prometheus.MustNewConstMetric(c.nomStake, prometheus.GaugeValue, data.NomStake, baseLabels...)
		ch <- prometheus.MustNewConstMetric(c.eraPoints, prometheus.GaugeValue, data.EraPoints, baseLabels...)
		ch <- prometheus.MustNewConstMetric(c.sessPoints, prometheus.GaugeValue, data.SessPoints, baseLabels...)
		ch <- prometheus.MustNewConstMetric(c.backPoints, prometheus.GaugeValue, data.BackPoints, baseLabels...)

		for paraID, pts := range data.ParaPoints {
			pLabels := append([]string{}, baseLabels...)
			pLabels = append(pLabels, paraID)
			ch <- prometheus.MustNewConstMetric(c.paraBackingPoints, prometheus.GaugeValue, pts, pLabels...)
		}
	}
}
