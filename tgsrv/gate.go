package tgsrv

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Gate struct {
	Phones              map[string]bool
	RestrictedPhones    map[string]bool
	BluetoothMacNames   map[string]string
	IgnoreBluetoothMacs map[string]bool
	GateUrl             string
	TelegramUrl         string
	TelegramChatId      string
	TelegramTimeoutSec  int
	ProxyUrl            string
	User                string
	Password            string
	phoneCalls          chan string
	bleTrackings        chan *BLETracking
}

func (g *Gate) Init() {
	g.phoneCalls = make(chan string)
	g.bleTrackings = make(chan *BLETracking)
}

func (g *Gate) handlingCalls(abort chan struct{}) {
	var gateTime time.Time
Loop:
	for {
		select {
		case phone := <-g.phoneCalls:
			phone = strings.TrimPrefix(phone, "+")
			v, ok := g.Phones[phone]
			if !ok {
				g.sendToTelegram(fmt.Sprintf("%s uknown", phone))
				continue
			}
			if !v || g.RestrictedPhones[phone] {
				g.sendToTelegram(fmt.Sprintf("%s restricted", phone))
				continue
			}
			if time.Since(gateTime) < 10*time.Second {
				g.sendToTelegram(fmt.Sprintf("%s ок, opening already in action", phone))
				continue
			}
			gateTime = time.Now()
			err := g.sendOpenCommandToGate(phone)
			if err != nil {
				g.sendToTelegram(fmt.Sprintf("%s ок, %v", phone, err))
			} else {
				g.sendToTelegram(fmt.Sprintf("%s ок", phone))
			}
		case <-abort:
			break Loop
		}
	}
}

func (g *Gate) sendToTelegram(msg string) {
	var client *http.Client
	if len(g.ProxyUrl) != 0 {
		proxyURL, err := url.Parse(g.ProxyUrl)
		if err != nil {
			Logger.Errorf("error calling telegram: %v", err)
			return
		}
		client = &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	} else {
		client = &http.Client{}
	}
	formData := url.Values{
		"chat_id": {g.TelegramChatId},
		"text":    {msg + time.Now().Format(" (2006-01-02 15:04:05)")},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(g.TelegramTimeoutSec)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", g.TelegramUrl, strings.NewReader(formData.Encode()))
	if err != nil {
		Logger.Errorf("error calling telegram: %v", err)
		return
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		Logger.Errorf("error calling telegram: %v", err)
	} else {
		defer resp.Body.Close()
	}
}

func (g *Gate) sendOpenCommandToGate(phone string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf(g.GateUrl, phone), strings.NewReader(""))
	if err != nil {
		return err
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(g.User, g.Password)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		Logger.Errorf("%s error calling gate: %v", phone, err)
	} else {
		Logger.Infof("%s http %d", phone, resp.StatusCode)
		defer resp.Body.Close()
	}
	return err
}

func (g *Gate) handlingBLETracking(abort chan struct{}) {
	var firstWaitIsOver <-chan time.Time
	var nextWaitIsOver <-chan time.Time
	const firstDuration = 3 * time.Second
	const nextDuration = 60 * time.Second
	ticker := time.NewTicker(firstDuration)
	var tt []*BLETracking
Loop:
	for {
		select {
		case t := <-g.bleTrackings:
			if g.IgnoreBluetoothMacs[t.MAC] {
				continue
			}
			tt = append(tt, t)
			if firstWaitIsOver == nil && nextWaitIsOver == nil {
				ticker.Reset(firstDuration)
				firstWaitIsOver = ticker.C
			}

		case <-firstWaitIsOver:
			ticker.Reset(nextDuration)
			firstWaitIsOver = nil
			nextWaitIsOver = ticker.C
			g.sendToTelegramMsg(tt)
			tt = tt[:0]

		case <-nextWaitIsOver:
			if len(tt) == 0 {
				nextWaitIsOver = nil
				continue
			}
			g.sendToTelegramMsg(tt)
			tt = tt[:0]

		case <-abort:
			break Loop
		}
	}
}

func (g *Gate) sendToTelegramMsg(tt []*BLETracking) {
	var msg strings.Builder
	for _, t := range tt {
		mac := g.BluetoothMacNames[t.MAC]
		if len(mac) == 0 {
			mac = t.MAC
		}
		msg.WriteString(mac)
		if len(t.Name) != 0 {
			msg.WriteString(" ")
			msg.WriteString(t.Name)
		}
		if t.RSSI != 0 {
			msg.WriteString(" RSSI:")
			msg.WriteString(strconv.Itoa(t.RSSI))
		}
		msg.WriteString("\n")
	}
	g.sendToTelegram(msg.String())
}
