package tgsrv

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Gate struct {
	Phones           map[string]bool
	RestrictedPhones map[string]bool
	PhoneCalls       chan string
	GateUrl          string
	TelegramUrl      string
	TelegramChatId   string
	User             string
	Password         string
	phoneCalls       chan string
}

func (g *Gate) Init() {
	g.phoneCalls = make(chan string)
}

func (g *Gate) handlingCalls(abort chan struct{}) {
	var gateTime time.Time
Loop:
	for {
		select {
		case phone := <-g.PhoneCalls:
			phone = strings.TrimPrefix(phone, "+")
			if !g.Phones[phone] {
				g.sendToTelegram(fmt.Sprintf("%s uknown", phone))
				continue
			}
			if g.RestrictedPhones[phone] {
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
				g.sendToTelegram(fmt.Sprintf("%s ок, %v", err))
			} else {
				g.sendToTelegram(fmt.Sprintf("%s ок", phone))
			}
		case <-abort:
			break Loop
		}
	}
}

func (g *Gate) sendToTelegram(msg string) {
	formData := url.Values{
		"chat_id": {g.TelegramChatId},
		"text":    {msg},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", g.TelegramUrl, strings.NewReader(formData.Encode()))
	if err != nil {
		panic(err)
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		Logger.Errorf("error calling telegram: %v", err)
	}
	defer resp.Body.Close()
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
		Logger.Errorf("error calling gate: %v", err)
	}
	defer resp.Body.Close()
	return err
}
