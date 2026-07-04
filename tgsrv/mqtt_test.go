package tgsrv

import (
	"7stgbot/config"
	"testing"

	"github.com/BurntSushi/toml"
)

const cfgStr = `
[MQTT]

[MQTT.F]
URL = "wss://portal.pal-es.com/mqtt"
TopicsHex = "f6013946001b6f72672f31353437392f6576742f6465766963652f63726561746501002861646d696e2f6d6b3472656740676d61696c2e636f6d2f6576742f646173682f73657474696e677301002d61646d696e2f6d6b3472656740676d61696c2e636f6d2f6576742f646173682f726563656e74446576696365730100276f72672f31353437392f6576742f6465766963652f34473630303231353537352f7570646174650100276f72672f31353437392f6576742f6465766963652f34473630303231353537352f64656c6574650100246f72672f31353437392f6576742f6465766963652f34473630303231353537352f6c6f6701"
Username = "adminApp"
ClientID = "mk4reg@gmail.com"
ClientIDPostfix = "m58d24"

[MQTT.Headers]
User-Agent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"
Origin = "https://portal.pal-es.com"
Accept-Encoding = "gzip, deflate, br, zstd"
Accept-Language = "en-US,en;q=0.9,ru-RU;q=0.8,ru;q=0.7"
Cookie = "consent-policy=%7B%22ess%22%3A1%2C%22func%22%3A1%2C%22anl%22%3A1%2C%22adv%22%3A1%2C%22dt3%22%3A1%2C%22ts%22%3A29493585%7D; _ga=GA1.1.2013323183.1769615148; _ga_M5P4HN0KW0=GS2.1.s1782399141$o6$g1$t1782399142$j59$l0$h0"

`

func TestMQTTConfig(t *testing.T) {
		var cfg config.Config
	_, err := toml.Decode(cfgStr, &cfg)
	//    err := toml.Unmarshal([]byte(tomlData), &cfg)
	if err != nil {
		t.Fatalf("Ошибка парсинга TOML: %v", err)
	}
}