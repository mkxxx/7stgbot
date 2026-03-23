package tgsrv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Gate struct {
	Phones               map[string]bool
	RestrictedPhones     map[string]bool
	BluetoothMacNames    map[string]string
	IgnoreBluetoothMacs  map[string]bool
	GateUrl              string
	TelegramUrl          string
	TelegramChatId       string
	TelegramTimeoutSec   int
	ProxyUrl             string
	User                 string
	Password             string
	phoneCalls           chan PhoneCall
	bleTrackings         chan *BLETracking
	PalesPortalUser      string
	PalesPortalPwd       string
	palesPortalUserToken string
	palesLogsStartDate   int64
}

type PalesLoginResp struct {
	Msg  string
	User struct {
		Token string
	}
}

type PalesLog struct {
	Msg string
	Log struct {
		Count int
		List  []*PalesLogUser
	}
}

type PalesLogUser struct {
	UserId    string
	Sn        string
	Approved  bool
	Type      int
	Tm        int64
	Reason    int
	Firstname string
	Lastname  string
}

type PalesUsers struct {
	Msg   string
	Users struct {
		Count int
		List  []*PalesUser
	}
}

type PalesUser struct {
	Id                  string `json:"_id"`
	Firstname           string
	Lastname            string
	Admin               bool
	Output1             bool
	StartDate           string
	EndDate             string
	DialToOpen          bool
	LocalOnly           bool
	Output1Latch        bool
	Output2Latch        bool
	Output1LatchMaxTime int64
	Output2LatchMaxTime int64
	SecondaryDevice     bool
	Notifications       bool
	GuestInvitation     bool
	GeoFence1           struct {
		Enabled             bool
		Lat                 float64
		Long                float64
		Radius              int
		Rssi                int
		Key                 string
		ConfirmNotification bool
		RetrySeconds        int `json:"retry"`
	}
}

func (u *PalesLogUser) timestamp() string {
	return time.Unix(u.Tm, 0).In(Location).Format("2006-01-02 15:04:05")
}

func (u *PalesLogUser) typeName() string {
	switch u.Type {
	case 1:
		return "call"
	case 100:
		return "inet"
	case 108:
		return "nearby"
	case 2:
		return "remote"
	case 8:
		return "bt"
	}
	return fmt.Sprintf("unknown %d", u.Type)
}

func (g *Gate) Init() {
	g.phoneCalls = make(chan PhoneCall)
	g.bleTrackings = make(chan *BLETracking)
}

func (g *Gate) handlingCalls(abort chan struct{}) {
	var gateTime time.Time
Loop:
	for {
		select {
		case call := <-g.phoneCalls:
			phone := strings.TrimPrefix(call.Phone, "+")
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
			elapsed := time.Since(call.time())
			if elapsed > 20*time.Second {
				g.sendToTelegram(fmt.Sprintf("%s ок, but call is overdue %d s", phone, elapsed/time.Second))
				continue
			}
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
		"text":    {msg + time.Now().In(Location).Format("2006-01-02 15:04:05")},
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

func (g *Gate) palesLoginAndLoadLoop(abort chan struct{}) {
	g.login()
	g.palesLogsStartDate = time.Now().Unix()
	g.loadPalesLogs()
	minuteTicker := time.NewTicker(time.Minute)
	tenSecTicker := time.NewTicker(10 * time.Second)
	thirtyMinuteTicker := time.NewTicker(30 * time.Minute)
Loop:
	for {
		select {
		case <-minuteTicker.C:
			g.login()
		case <-tenSecTicker.C:
			g.loadPalesLogs()
		case <-thirtyMinuteTicker.C:
		case <-abort:
			break Loop
		}
	}
}

func (g *Gate) login() {
	if len(g.palesPortalUserToken) != 0 {
		return
	}
	type loginForm struct {
		Username string `json:"username"`
		Password string `json:"password"`
		B        string `json:"b"`
	}
	form := loginForm{Username: g.PalesPortalUser, Password: g.PalesPortalPwd, B: ""}
	jsonData, err := json.Marshal(form)
	if err != nil {
		Logger.Errorf("pal-es login: %v", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", "https://portal.pal-es.com/api1/user/login1", bytes.NewBuffer(jsonData))
	if err != nil {
		Logger.Errorf("pal-es login: %v", err)
		return
	}
	req.Header.Add("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		Logger.Errorf("pal-es login http: %v", err)
		return
	}
	defer resp.Body.Close()
	Logger.Infof("pal-es login http %d", resp.StatusCode)
	if resp.StatusCode != http.StatusOK {
		return
	}
	var result PalesLoginResp
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		Logger.Errorf("error unmarshalling pal-es login http response: %v", err)
		return
	}
	Logger.Infof("pal-es login %s", result.Msg)
	g.palesPortalUserToken = result.User.Token
}

func (g *Gate) loadPalesLogs() {
	if len(g.palesPortalUserToken) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("https://portal.pal-es.com/api1/device/4G600211776/log?skip=0&limit=20&filter=&startDate=%d&endDate=&approved=&reasons=&rly=&type=",
			g.palesLogsStartDate), nil)

	if err != nil {
		Logger.Errorf("%v", err)
		return
	}
	req.Header.Add("x-access-token", g.palesPortalUserToken)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		Logger.Errorf("pal-es log http: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		Logger.Infof("pal-es log http %d", resp.StatusCode)
		return
	}
	Logger.Debugf("pal-es log http %d", resp.StatusCode)
	var result PalesLog
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		Logger.Errorf("error unmarshalling pal-es log http response: %v", err)
		return
	}
	Logger.Debugf("pal-es log %s, %d", result.Msg, len(result.Log.List))
	if len(result.Log.List) == 0 {
		return
	}
	var msg strings.Builder
	for _, l := range result.Log.List {
		g.palesLogsStartDate = max(g.palesLogsStartDate, l.Tm)
		var approved string
		if !l.Approved {
			approved = fmt.Sprintf("!OK %d", l.Reason)
		}
		msg.WriteString(fmt.Sprintf("%s %s %s %s %s%s %s \n", l.timestamp(), l.typeName(), l.UserId, l.Sn,
			l.Firstname, l.Lastname, approved))
	}
	Logger.Infof("received %d pal-es log records", len(result.Log.List))
	g.palesLogsStartDate++
	g.sendToTelegram(msg.String())
}
