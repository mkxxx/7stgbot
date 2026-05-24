package tgsrv

import (
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	URL              = "wss://portal.pal-es.com/mqtt"
	MQTT_CONNECT_HEX = "e20200044d51545404c2001e00176d6b3472656740676d61696c2e636f6d3a6d3538643234000861646d696e417070013365794a30655841694f694a4b563151694c434a68624763694f694a49557a55784d694a392e65794a6c654841694f6a45334e7a63314e5455794f444d324d545973496d6c6b496a6f6962577330636d566e5147647459576c734c6d4e7662534973496d4e705a434936496a46424d6b517a4e4459324e6a5932516a49315154677a52555a4649697769616d6c30496a6f694d475a6c4d6a4e6b4f574574596a63794e6930304d4463314c5746694e4463745a6d55335a4759314e5749794e44497749697769616949364d537769595349364d537769637949364d48302e34753576484b326c7547424233564f6f484635484474714e6b4b78664c4f364263734d44784e7675414f7a6833794f66627651626e785379592d774a52646e527373526c2d376c6a6f516a3241707465427851326c51" // Вставьте полный HEX
	TOPICS_HEX       = "20c4ea001b6f72672f31353437392f6576742f6465766963652f63726561746501822dc4eb002861646d696e2f6d6b3472656740676d61696c2e636f6d2f6576742f646173682f73657474696e6773018232c4ec002d61646d696e2f6d6b3472656740676d61696c2e636f6d2f6576742f646173682f726563656e744465766963657301822cc4ed00276f72672f31353437392f6576742f6465766963652f34473630303231313737362f75706461746501822cc4ee00276f72672f31353437392f6576742f6465766963652f34473630303231313737362f64656c657465018229c4ef00246f72672f31353437392f6576742f6465766963652f34473630303231313737362f6c6f6701"
	COOKIES          = "consent-policy=%7B%22ess%22%3A1%2C%22func%22%3A1%2C%22anl%22%3A1%2C%22adv%22%3A1%2C%22dt3%22%3A1%2C%22ts%22%3A29493585%7D; _ga=GA1.1.2013323183.1769615148; _ga_M5P4HN0KW0=GS2.1.s1775193549$o4$g1$t1775193616$j60$l0$h0"
	MQTT_ERROR       = "MQTT_ERROR"
)

func (g *Gate) listenPalESMQTT(abort chan struct{}, topicEvents chan string) {
	for {
		token := g.PalESPortalUserToken.Load().(string)
		if token == "" {
			time.Sleep(time.Second)
			continue
		}
		t := time.Now()
		g.connectAndReadPalESMQTT(token, abort, topicEvents)
		select {
		case <-abort:
			return
		default:
		}
		if time.Since(t) < 10*time.Second {
			time.Sleep(10 * time.Second)
		}
	}
}

func (g *Gate) connectAndReadPalESMQTT(token string, abort chan struct{}, topicEvents chan string) {
	//token := "eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzUxMiJ9.eyJleHAiOjE3Nzc1NTUyODM2MTYsImlkIjoibWs0cmVnQGdtYWlsLmNvbSIsImNpZCI6IjFBMkQzNDY2NjY2QjI1QTgzRUZFIiwiaml0IjoiMGZlMjNkOWEtYjcyNi00MDc1LWFiNDctZmU3ZGY1NWIyNDIwIiwiaiI6MSwiYSI6MSwicyI6MH0.4u5vHK2luGBB3VOoHF5HDtqNkKxfLO6BcsMDxNvuAOzh3yOfbvQbnxSyY-wJRdnRssRl-7ljoQj2ApteBxQ2lQ"
	header := http.Header{}
	header.Add("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36")
	header.Add("Origin", "https://portal.pal-es.com")
	header.Add("Cookie", COOKIES)
	header.Add("Accept-Language", "en-US,en;q=0.9,ru-RU;q=0.8,ru;q=0.7")

	dialer := websocket.Dialer{
		Subprotocols: []string{"mqtt"},
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}
	conn, _, err := dialer.Dial(URL, header)
	if err != nil {
		Logger.Errorf("dial %s error: %v", URL, err)
		topicEvents <- MQTT_ERROR
		return
	}
	defer conn.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-abort:
			conn.Close()
		case <-done:
		}
	}()
	// Отправка "магического байта" 0x10
	err = conn.WriteMessage(websocket.BinaryMessage, []byte{0x10})
	if err != nil {
		Logger.Errorf("mqtt error:", err)
		return
	}
	// Отправка MQTT CONNECT
	//clientIDPostfix := generateRandomID(7)
	clientIDPostfix := "m58d24"
	clientID := fmt.Sprintf("%s:%s", "mk4reg@gmail.com", clientIDPostfix) // zr2wskv,m58d24,6ydjgmt
	username := "adminApp"
	packet := buildConnectPacket(clientID, username, token)
	err = conn.WriteMessage(websocket.BinaryMessage, packet)
	if err != nil {
		Logger.Errorf("mqtt connect error:", err)
		return
	}
	_, message, err := conn.ReadMessage()
	if err != nil {
		Logger.Errorf("mqtt read error: %v", err)
		return
	}
	resHex := hex.EncodeToString(message)
	if resHex != "20020000" {
		Logger.Errorf("mqtt login denied: %s", resHex)
		return
	}
	// Отправляем подписку
	err = conn.WriteMessage(websocket.BinaryMessage, []byte{0x82})
	if err != nil {
		Logger.Errorf("mqtt write error:", err)
		return
	}
	topicPayload, _ := hex.DecodeString(TOPICS_HEX)
	err = conn.WriteMessage(websocket.BinaryMessage, topicPayload)
	if err != nil {
		Logger.Errorf("mqtt write error:", err)
		return
	}
	go startPinger(conn, abort)

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			Logger.Errorf("mqtt read error: %v", err)
			return
		}
		resHex := hex.EncodeToString(message)
		if resHex == "d000" { // PINGRESP
			continue
		}
		topic, data := parsePublish(message)
		if topic != "" {
			msg := fmt.Sprintf("mqtt read: %s %s", topic, hex.EncodeToString(data))
			Logger.Info(msg)
			topicEvents <- topic
			g.sendSystemNotification(msg)
		} else {
			Logger.Infof("mqtt read: %s", resHex)
		}
	}
}

func startPinger(conn *websocket.Conn, done chan struct{}) {
	ticker := time.NewTicker(20 * time.Second)
	for {
		select {
		case <-ticker.C:
			err := conn.WriteMessage(websocket.BinaryMessage, []byte{0xc0, 0x00})
			if err != nil {
				Logger.Errorf("mqtt write error:", err)
				return
			}
		case <-done:
			return
		}
	}
}

func buildConnectPacket(clientID, username, jwtToken string) []byte {
	packetBody := []byte{0x00, 0x04, 0x4d, 0x51, 0x54, 0x54, 0x04, 0xc2, 0x00, 0x1e}

	cIDBytes := []byte(clientID)
	lenBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(lenBuf, uint16(len(cIDBytes)))
	packetBody = append(packetBody, lenBuf...)
	packetBody = append(packetBody, cIDBytes...)

	uBytes := []byte(username)
	binary.BigEndian.PutUint16(lenBuf, uint16(len(uBytes)))
	packetBody = append(packetBody, lenBuf...)
	packetBody = append(packetBody, uBytes...)

	// 5. Добавление Password (3 + JWT)
	// Важно: Пал-ес хочет, чтобы в поле пароля первым символом была '3'
	password := append([]byte("3"), []byte(jwtToken)...)
	binary.BigEndian.PutUint16(lenBuf, uint16(len(password)))
	packetBody = append(packetBody, 1)
	packetBody = append(packetBody, password...)

	// 6. Формирование финального пакета с заголовком e2 02
	// e2 02 — это 290 байт (длина нашего packetBody)
	return append([]byte{0xe2, 0x02}, packetBody...)
}

/*
returns topic,data
topic ex: org/15479/evt/device/4G600211776/update, org/15479/evt/device/4G600211776/log
*/
func parsePublish(message []byte) (string, []byte) {
	if len(message) < 4 || message[0] != 0x30 {
		return "", nil
	}
	topicLen := binary.BigEndian.Uint16(message[2:4])
	topic := string(message[4 : 4+topicLen])
	// Всё, что осталось после топика — это Payload (данные)
	payload := message[4+topicLen:]
	return topic, payload
}

func generateRandomID(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	// Инициализируем генератор случайных чисел текущим временем
	seededRand := rand.New(rand.NewSource(time.Now().UnixNano()))
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[seededRand.Intn(len(charset))]
	}
	return string(b)
}
