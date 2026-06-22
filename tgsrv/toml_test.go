package tgsrv

import (
	"7stgbot/config"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	//"github.com/pelletier/go-toml/v2"
)

const tomlData = `
port = 8084
staticDir = "/home/mk/work/7s-static/public"
tgToken = "5518888888:AAH"
DiscordAlertChannelURL = "https://discord.com/api/webhooks/927562777759977493/rRNpQ"
IfTTTKey = "tZZmolUk"
AdminEmails = ["mmm@gmail.com","ppp@yandex.ru","iii@yandex.ru"]
AdminPhone = "+79261234567"
GateRelayOnOffUrl = "http://10.66.66.3/text/Relay_With_Message/set?value=%s"
GateRelayOnUrl = "http://10.66.66.3/text/Relay_Turn_On/set?value=%s"
GateRelayOffUrl = "http://10.66.66.3/text/Relay_Turn_Off/set?value=%s"
GateRelayGetUrl = "http://10.66.66.3/switch/Main%20Relay"
TelegramUrl = "https://api.telegram.org/bot551:AAHVFsEYIa/sendMessage"
TelegramChatId = "-5233"
TelegramTimeoutSec = 5
ProxyUrl = "http://MFWD:G3xD@77.222.111.203:1234"
GateUser = "user"
GatePwd = "password"
PalesPortalUser = "mmm@gmail.com"
PalesPortalPwd = "bbb"
GateOpenNumber = "+79961234567"
GateInfoNumber = "+79991234567"
BLEPeriodSec = 40
KeypadHitLimit                = 5
KeypadHitLimitDurationMinutes = 1
KeypadThrottleMinutes         = 3
NtfyURL = "http://localhost:8081"
NtfyToken = "tk_u"
LogsTikerMinutes = 10
BLEAutoOpenLagMin = 10

[BTMacSystem]
"CA:7E:07:37:41:48" = "pal-es spider i-wr"
"CA:7E:07:37:41:4E" = "pal-es spider i-wr"
"CA:7E:07:37:41:4F" = "pal-es spider i-wr"

[BTMacIgnore]
"E4:AE:E4:50:2A:D5" = "TUYA_ градусник"

[BTMacAutoOpenGate]
"5B:BB:2D:AD:B1:E7" = "79851234567"

[WiFiMACAutoOpenGate]
"22:7c:bb:54:7d:22" = "79261234567"

[BTMacNames]
"C1:58:53:EB:43:32" = "TD_707223 помойка"

[WiFiMacNames]
"ce:09:55:75:33:1b" = "Сергей 79991234567"

[Coef]
202201 = 8.7
202206 = 10.2

[Price]
202201 = 5.93
202207 = 6.17

[SMSRateLimiterCfg]
5s = 1
1h = 100

[qr]
Name = "СНТ Семиславка"
PersonalAcc = "40703810200000000815"
BankName = "ПАО ПРОМСВЯЗЬБАНК"
BIC = "044525555"
CorrespAcc = "30101810400000000555"
PayeeINN = "5005017857"

[OpenSchedule]

[MaskedPhones]
"681234567" = "79681234567"

[Gate]
IP = "10.66.66.3"
user = "user"
pwd = "password"

[Gate.Relay]
OnOffTextName = "Relay_With_Message"
OnTextName = "Relay_Turn_On"
OffTextName = "Relay_Turn_Off"
SwitchName = "Main%20Relay"

`

func TestTOMLConfig(t *testing.T) {

	type Rate struct {
		Ticker time.Duration
		Cnt    int
	}

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
		GateRelayOnOffUrl             string
		GateRelayOnUrl                string
		GateRelayOffUrl               string
		GateRelayGetUrl               string
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
		KeypadReleased                bool // remove
		NtfyURL                       string
		NtfyToken                     string
		OpenSchedule                  map[string]int // "09:00"=10\n"10:30"=5\n"22:00"=15   "hh:mm"=number_of_minutes
		BLEAutoOpenLagMin             int64
		BTMacSystem                   map[string]string
		BTMacIgnore                   map[string]string
		BTMacAutoOpenGate             map[string]string
		BTMacNames                    map[string]string
		WiFiMACAutoOpenGate           map[string]string
		WiFiMacNames                  map[string]string
		MaskedPhones                  map[string]string
		LogsTikerMinutes              int64
	}

	var cfg Config
	_, err := toml.Decode(tomlData, &cfg)
	//    err := toml.Unmarshal([]byte(tomlData), &cfg)
	if err != nil {
		t.Fatalf("Ошибка парсинга TOML: %v", err)
	}
	{
		got := cfg.LogsTikerMinutes
		want := int64(10)
		if want != got {
			t.Errorf("got %v, want %v", got, want)
		}
	}
	{
		got := cfg.Gate.Pwd
		want := "password"
		if want != got {
			t.Errorf("got %v, want %v", got, want)
		}
	}
	{
		got := cfg.Gate.Relay.OnTextName
		want := "Relay_Turn_On"
		if want != got {
			t.Errorf("got %v, want %v", got, want)
		}
	}
}

func TestTOMLConfig2(t *testing.T) {
	var cfg config.Config
	_, err := toml.Decode(tomlData, &cfg)
	//    err := toml.Unmarshal([]byte(tomlData), &cfg)
	if err != nil {
		t.Fatalf("Ошибка парсинга TOML: %v", err)
	}
	{
		got := cfg.LogsTikerMinutes
		want := int64(10)
		if want != got {
			t.Errorf("got %v, want %v", got, want)
		}
	}
	{
		got := cfg.Gate.Pwd
		want := "password"
		if want != got {
			t.Errorf("got %v, want %v", got, want)
		}
	}
	{
		got := cfg.Gate.Relay.OnTextName
		want := "Relay_Turn_On"
		if want != got {
			t.Errorf("got %v, want %v", got, want)
		}
	}
	{
		got := cfg.GateRelayTextGetURL(cfg.Gate.Relay.OnTextName)
		want := "http://10.66.66.3/text/Relay_Turn_On"
		if want != got {
			t.Errorf("got %v, want %v", got, want)
		}
	}
	{
		got := cfg.GateRelayTextPostURL(cfg.Gate.Relay.OnTextName, "text")
		want := "http://10.66.66.3/text/Relay_Turn_On/set?value=text"
		if want != got {
			t.Errorf("got %v, want %v", got, want)
		}
	}
}
