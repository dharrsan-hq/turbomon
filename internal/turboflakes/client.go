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
}

type ValidatorProfile struct {
	NominatorsCounter  int     `json:"nominators_counter"`
	NominatorsRawStake float64 `json:"nominators_raw_stake"`
}

func NewClient(host string) *Client {
	return &Client{
		Host:       host,
		HttpClient: &http.Client{Timeout: 10 * time.Second},
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

	sessUrl := fmt.Sprintf("https://%s/api/v1/validators/%s?session=current&show_summary=true", c.Host, address)
	profUrl := fmt.Sprintf("https://%s/api/v1/validators/%s/profile", c.Host, address)

	if err := c.getJSON(sessUrl, &sData); err != nil {
		return nil, nil, err
	}
	if err := c.getJSON(profUrl, &pData); err != nil {
		return nil, nil, err
	}

	return &sData, &pData, nil
}

func (c *Client) getJSON(url string, target interface{}) error {
	resp, err := c.HttpClient.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("API %s returned %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(target)
}
