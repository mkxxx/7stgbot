package config

import (
	"fmt"
	"time"
)

type Config struct {
	Port                   int
	StaticDir              string
	TgToken                string
	Price                  map[string]float64
	Coef                   map[string]float64
	QR                     map[string]string
	DiscordAlertChannelURL string
	IfTTTKey               string
	AdminEmails            []string
	AdminPhone             string
	SMSRateLimiterCfg      map[string]int
	SMSRateLimiter         []Rate
	Gate                   struct {
		IP   string
		User string
		Pwd  string

		Relay struct {
			OnOffTextName string
			OnTextName    string
			OffTextName   string
			SwitchName    string
		}
	}
	TelegramUrl                   string
	TelegramChatId                string
	TelegramTimeoutSec            int
	ProxyUrl                      string
	PalesPortalUser               string
	PalesPortalPwd                string
	BleWatchLocation              int
	GateOpenNumber                string
	GateInfoNumber                string
	BLEResumeAbsenceDurationSec   int64
	BLEPeriodSec                  int64 // TODO remove
	KeypadHitLimit                int
	KeypadHitLimitDurationMinutes int64
	KeypadThrottleMinutes         int64
	NtfyURL                       string
	NtfyToken                     string
	OpenSchedule                  map[string]int // "09:00"=10\n"10:30"=5\n"22:00"=15   "hh:mm"=number_of_minutes
	BLEAutoOpenLagMin             int64          // TODO remove
	BTMacSystem                   map[string]string
	BTMacIgnore                   map[string]string
	BTMacAutoOpenGate             map[string]string
	BTMacNames                    map[string]string
	WiFiMACAutoOpenGate           map[string]string
	WiFiMacNames                  map[string]string
	MaskedPhones                  map[string]string
	LogsTikerMinutes              int64
	TestLocation                  int
	LogLocations                  map[string]bool
}

func (c *Config) GateRelayTextGetURL(name string) string {
	return fmt.Sprintf("http://%s/text/%s", c.Gate.IP, name)
}

func (c *Config) GateRelayTextPostURL(name, value string) string {
	return fmt.Sprintf("http://%s/text/%s/set?value=%s", c.Gate.IP, name, value)
}

func (c *Config) GateRelaySwitchGetURL(name string) string {
	return fmt.Sprintf("http://%s/switch/%s", c.Gate.IP, name)
}

type ConfigSubscription struct {
	Subscribers []chan *Config
}

func (h *ConfigSubscription) Subscribe() chan *Config {
	ch := make(chan *Config, 1)
	h.Subscribers = append(h.Subscribers, ch)
	return ch
}

type Rate struct {
	Ticker time.Duration
	Cnt    int
}

func (r *Rate) rateNano() time.Duration {
	return time.Duration(int(r.Ticker.Nanoseconds()) / r.Cnt)
}

type ByRate []Rate

func (r ByRate) Len() int      { return len(r) }
func (r ByRate) Swap(i, j int) { r[i], r[j] = r[j], r[i] }
func (r ByRate) Less(i, j int) bool {
	return r[i].rateNano() < r[j].rateNano()
}
