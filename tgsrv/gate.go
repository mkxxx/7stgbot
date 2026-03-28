package tgsrv

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
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
	phoneSmses           chan PhoneSms
	bleTrackings         chan *BLETracking
	PalesPortalUser      string
	PalesPortalPwd       string
	PalesTokenFilename   string
	PalesPortalUserToken string
	CfgDir               string
	palesLogsStartDate   int64
	BleWatchLocation     int
	mu                   sync.Mutex
	lastOpened           time.Time
	GateOpenNumber       string
	GateInfoNumber       string
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
		return "dial"
	case 100:
		return "inet"
	case 108:
		return "bt-auto"
	case 2:
		return "remote"
	case 8:
		return "bluetooth"
	}
	return fmt.Sprintf("unknown %d", u.Type)
}

func (g *Gate) Init() {
	g.phoneCalls = make(chan PhoneCall)
	g.phoneSmses = make(chan PhoneSms)
	g.bleTrackings = make(chan *BLETracking)
}

func (g *Gate) handlingCalls(abort chan struct{}) {
	var gateTime time.Time
Loop:
	for {
		select {
		case call := <-g.phoneCalls:
			phone := strings.TrimPrefix(call.Phone, "+")
			allowed, ok := g.Phones[phone]
			if ok && allowed {
				allowed = !g.RestrictedPhones[phone]
			}
			if call.CalledNumber == g.GateOpenNumber {
				if !ok {
					g.sendToTelegram(fmt.Sprintf("%s uknown", phone))
					continue
				}
				if !allowed {
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
				continue
			}
			if call.CalledNumber == g.GateInfoNumber {
				if !ok {
					g.sendToTelegram(fmt.Sprintf("%s не зарегистрирован", maskPhone(phone)))
					continue
				}
				if !allowed {
					g.sendToTelegram(fmt.Sprintf("%s проезд запрещен", maskPhone(phone)))
					continue
				}
				g.sendToTelegram(fmt.Sprintf("%s OK", maskPhone(phone)))
				continue
			}
		case <-abort:
			break Loop
		}
	}
}

func (g *Gate) handlingSmses(abort chan struct{}) {
Loop:
	for {
		select {
		case sms := <-g.phoneSmses:
			phone := strings.TrimPrefix(sms.Phone, "+")
			allowed, ok := g.Phones[phone]
			if ok && allowed {
				allowed = !g.RestrictedPhones[phone]
			}
			if !ok {
				g.sendToTelegram(fmt.Sprintf("%s uknown sender of SNS: %s", phone, sms.Sms))
				continue
			}
			if !allowed {
				g.sendToTelegram(fmt.Sprintf("%s restricted sender of SNS: %s", phone, sms.Sms))
				continue
			}
			g.sendToTelegram(fmt.Sprintf("%s %s sent SMS: %s", sms.timestampSent(), sms.Phone, sms.Sms))
			//smsText := cleanString(sms.Sms, ",")

		case <-abort:
			break Loop
		}
	}
}

func cleanString(str string, delimiters string) string {
	// Экранируем символы разделителей для регулярки
	d := regexp.QuoteMeta(delimiters)

	// 1. Удаляем пробелы вокруг разделителей: " , " -> ","
	reAround := regexp.MustCompile(fmt.Sprintf(`\s*([%s])\s*`, d))
	str = reAround.ReplaceAllString(str, "$1")

	// 2. Заменяем несколько пробелов между словами на один
	reMultiSpace := regexp.MustCompile(`\s+`)
	str = reMultiSpace.ReplaceAllString(str, " ")

	// 3. Убираем начальные и конечные пробелы
	return strings.TrimSpace(str)
}

func maskPhone(s string) string {
	rs := []rune(s) // Используем rune для корректной работы с кириллицей
	length := len(rs)
	if length <= 4 {
		return s
	}
	return strings.Repeat("*", length-4) + string(rs[length-4:])
}

func (g *Gate) sendToTelegram(msg string) {
	Logger.Infof("telegram: %s", msg)
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
		"text":    {msg + time.Now().In(Location).Format(" (2006-01-02 15:04:05)")},
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
			if t.Location != g.BleWatchLocation {
				continue
			}
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
	g.login(false)
	{
		st := g.loadPalesUsers()
		if st == http.StatusUnauthorized {
			g.login(true)
			g.loadPalesUsers()
		}
	}
	g.palesLogsStartDate = time.Now().Unix()
	g.loadPalesLogs(20 * time.Second)
	minuteLoginTicker := time.NewTicker(time.Minute)
	minuteLogsTicker := time.NewTicker(time.Minute)
	thirtyMinuteTicker := time.NewTicker(30 * time.Minute)
Loop:
	for {
		select {
		case <-minuteLoginTicker.C:
			g.login(false)
		case <-minuteLogsTicker.C:
			st := g.loadPalesLogs(20 * time.Second)
			if st == http.StatusUnauthorized {
				g.login(true)
				g.loadPalesLogs(20 * time.Second)
			}
		case <-thirtyMinuteTicker.C:
			g.loadPalesUsers()
		case <-abort:
			break Loop
		}
	}
}

func (g *Gate) login(force bool) {
	if force {
		g.PalesPortalUserToken = ""
	}
	if len(g.PalesPortalUserToken) != 0 {
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
	g.PalesPortalUserToken = result.User.Token

	err = os.WriteFile(g.PalesTokenFilename, []byte(result.User.Token), 0644)
	if err != nil {
		Logger.Errorf("error writing file %s %v", g.PalesTokenFilename, err)
	}
}

func (g *Gate) loadPalesLogs(timeout time.Duration) int {
	if len(g.PalesPortalUserToken) == 0 {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("https://portal.pal-es.com/api1/device/4G600211776/log?skip=0&limit=20&filter=&startDate=%d&endDate=&approved=&reasons=&rly=&type=",
			g.palesLogsStartDate), nil)

	if err != nil {
		Logger.Errorf("%v", err)
		return -1
	}
	req.Header.Add("x-access-token", g.PalesPortalUserToken)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		Logger.Errorf("pal-es log http: %v", err)
		return -1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		Logger.Infof("pal-es log http %d", resp.StatusCode)
		return resp.StatusCode
	}
	Logger.Debugf("pal-es log http %d", resp.StatusCode)
	var result PalesLog
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		Logger.Errorf("error unmarshalling pal-es log http response: %v", err)
		return -1
	}
	Logger.Debugf("pal-es log %s, %d", result.Msg, len(result.Log.List))
	if len(result.Log.List) == 0 {
		return 0
	}
	var msg strings.Builder
	for _, l := range result.Log.List {
		g.palesLogsStartDate = max(g.palesLogsStartDate, l.Tm)
		var approved string
		if !l.Approved {
			approved = fmt.Sprintf("!OK %d", l.Reason)
		}
		sn := ""
		if !strings.HasSuffix(l.UserId, l.Sn) {
			sn = l.Sn
		}
		msg.WriteString(fmt.Sprintf("%s %s %s %s %s%s %s \n", l.timestamp(), l.typeName(), l.UserId, sn,
			l.Firstname, l.Lastname, approved))
	}
	Logger.Infof("received %d pal-es log records", len(result.Log.List))
	g.palesLogsStartDate++
	g.sendToTelegram(msg.String())
	return resp.StatusCode
}

func (g *Gate) loadPalesUsers() int {
	if len(g.PalesPortalUserToken) == 0 {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://portal.pal-es.com/api1/device/4G600211776/users?skip=0&limit=10000&filter=", nil)

	if err != nil {
		Logger.Errorf("%v", err)
		return -1
	}
	req.Header.Add("x-access-token", g.PalesPortalUserToken)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		Logger.Errorf("pal-es users http: %v", err)
		return -1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		Logger.Infof("pal-es users http %d", resp.StatusCode)
		return resp.StatusCode
	}
	Logger.Debugf("pal-es users http %d", resp.StatusCode)
	var result PalesUsers
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		Logger.Errorf("error unmarshalling pal-es users http response: %v", err)
		return -1
	}
	Logger.Debugf("pal-es users %s, %d", result.Msg, len(result.Users.List))
	for _, u := range result.Users.List {
		g.Phones[u.Id] = u.DialToOpen
	}
	var records [][]string
	records = append(records, []string{"Phone number", "First name", "Last name",
		"Admin", "Linked device", "Output 1", "Time group", "Remote control sn",
		"Dial to open", "Dial number (read only)", "Nearby only", "Latch 1", "Notes"})
	for _, u := range result.Users.List {
		records = append(records, []string{u.Id, u.Firstname, u.Lastname,
			If(u.Admin, "TRUE", "FALSE"), If(u.SecondaryDevice, "TRUE", "FALSE"), If(u.Output1, "TRUE", "FALSE"), "", "",
			If(u.DialToOpen, "TRUE", "FALSE"), "FALSE", "FALSE", If(u.Output1Latch, "TRUE", "FALSE"), ""})
	}
	fileName := filepath.Join(g.CfgDir, "pales_users.csv")
	f, err := os.Create(fileName)
	if err != nil {
		Logger.Errorf("error creating file %s  %v", fileName, err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	err = w.WriteAll(records)
	if err != nil {
		Logger.Errorf("error writing csv to file %s  %v", fileName, err)
	}
	return resp.StatusCode
}

func If[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
}

func (g *Gate) gateOpened() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if time.Since(g.lastOpened) < time.Minute {
		return
	}
	g.lastOpened = time.Now()
	Logger.Infof("gate opened")
	g.sendToTelegram("gate opened")
}
