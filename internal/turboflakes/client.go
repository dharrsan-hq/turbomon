package turboflakes

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Client struct {
	Host       string
	HttpClient *http.Client
}

type GlobalSession struct {
	Six int `json:"six"`
	Eix int `json:"eix"`
}

// NEW: Struct to capture the points for a specific parachain
type ParaStat struct {
	Pt int `json:"pt"`
}

type ValidatorSession struct {
	IsAuth bool `json:"is_auth"`
	IsPara bool `json:"is_para"`
	Auth   struct {
		Ep int   `json:"ep"`
		Ab []int `json:"ab"`
	} `json:"auth"`
	ParaSummary struct {
		Pt int `json:"pt"`
	} `json:"para_summary"`
	// Ensure this is exactly here, at the top level of the struct
	ParaStats map[string]ParaStat `json:"para_stats"`
}

type ValidatorProfile struct {
	NominatorsCounter  int     `json:"nominators_counter"`
	NominatorsRawStake float64 `json:"nominators_raw_stake"`
}

func NewClient(host string) *Client {
	return &Client{
		Host: host,
		// SRE Fix: Increased timeout to 20s to prevent context deadline drops
		HttpClient: &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *Client) GetGlobalSession() (*GlobalSession, error) {
	var gs GlobalSession
	url := fmt.Sprintf("https://%s/api/v1/sessions/current", c.Host)
	err := c.getJSON(url, &gs)
	return &gs, err
}

func (c *Client) GetValidatorData(address string) (*ValidatorSession, *ValidatorProfile, error) {
	var sData ValidatorSession
	var pData ValidatorProfile

	sessUrl := fmt.Sprintf("https://%s/api/v1/validators/%s?session=current&show_summary=true&show_stats=true", c.Host, address)
	profUrl := fmt.Sprintf("https://%s/api/v1/validators/%s/profile", c.Host, address)

	if err := c.getJSON(sessUrl, &sData); err != nil {
		return nil, nil, err
	}
	if err := c.getJSON(profUrl, &pData); err != nil {
		return nil, nil, err
	}

	return &sData, &pData, nil
}

// SRE Fix: Added exponential backoff and retry logic
func (c *Client) getJSON(url string, target interface{}) error {
	var lastErr error
	maxRetries := 3

	for i := 0; i < maxRetries; i++ {
		resp, err := c.HttpClient.Get(url)
		if err != nil {
			lastErr = err
			// Exponential backoff: 1s, 2s, 4s...
			time.Sleep(time.Duration(1<<i) * time.Second)
			continue
		}

		defer resp.Body.Close()

		if resp.StatusCode == 200 {
			return json.NewDecoder(resp.Body).Decode(target)
		}

		// Handle explicit Rate Limits
		if resp.StatusCode == 429 {
			time.Sleep(5 * time.Second)
		}

		lastErr = fmt.Errorf("API %s returned %d", url, resp.StatusCode)
	}

	return fmt.Errorf("after %d attempts, last error: %v", maxRetries, lastErr)
}
