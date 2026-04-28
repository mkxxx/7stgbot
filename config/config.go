package config

import (
	"time"
)

type Config struct {
	Port                          int
	StaticDir                     string
	TgToken                       string
	Price                         map[string]float64
	Coef                          map[string]float64
	QR                            map[string]string
	DiscordAlertChannelURL        string
	IfTTTKey                      string
	AdminEmails                   []string
	AdminPhone                    string
	SMSRateLimiterCfg             map[string]int
	SMSRateLimiter                []Rate
	GateUrl                       string
	TelegramUrl                   string
	TelegramChatId                string
	TelegramTimeoutSec            int
	ProxyUrl                      string
	GateUser                      string
	GatePwd                       string
	PalesPortalUser               string
	PalesPortalPwd                string
	BleWatchLocation              int
	GateOpenNumber                string
	GateInfoNumber                string
	BLEPeriodSec                  int64
	KeypadHitLimit                int
	KeypadHitLimitDurationMinutes int64
	KeypadThrottleMinutes         int64
	KeypadReleased                bool
	NtfyURL                       string
	NtfyToken                     string
	OpenSchedule                  map[string]int // "09:00"=10\n"10:30"=5\n"22:00"=15   "hh:mm"=number_of_minutes
	BLEAutoOpenLagMin             int64
	BTMacSystem                   map[string]string
	BTMacIgnore                   map[string]string
	BTMacAutoOpenGate             map[string]string
	BTMacNames                    map[string]string
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
