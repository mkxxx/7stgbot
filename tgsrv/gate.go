package tgsrv

import (
	"7stgbot/gate"
	"bytes"
	"cmp"
	"context"
	"crypto/aes"
	"crypto/cipher"
	crand "crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AnthonyHewins/gotfy"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

type Gate struct {
	Phones                 map[string]*PalesUser
	RestrictedPhones       map[string]bool
	GateUrl                string
	TelegramUrl            string
	TelegramChatId         string
	TelegramTimeoutSec     int
	ProxyUrl               string
	User                   string
	Password               string
	phoneCalls             chan *PhoneCall
	phoneSmses             chan *PhoneSms
	bleTrackings           chan *BLETracking
	openedEvets            chan OpenTime
	PalesPortalUser        string
	PalesPortalPwd         string
	PalesTokenFilename     string
	PalesPortalUserToken   string
	CfgDir                 string
	palesLastLog           *PalesLogUser
	lastOpenedNotification time.Time
	lastOpenCommandTime    atomic.Int64
	lastOpenedTime         atomic.Int64
	lastBLEOpenCommandTime map[string]time.Time
	bleTimes               map[string]time.Time
	GateOpenNumber         string
	GateInfoNumber         string
	BTMacs                 BTMacs
	palEsTimeGroups        *PalEsTimeGroups
	BLEPeriodSec           time.Duration
	RateWatcher            *RateWatcher
	PendingCalls           chan *gate.Call
	PendingSMSes           chan *gate.SMS
	KeypadCodesRequests    chan *PhoneSms
	SMSes                  gate.SMSesDAO
	KeypadCodes            gate.KeypadCodesDAO
	TOTPPhones             gate.TOTPPhonesDAO
	MattermostUsers        gate.MattermostUsersDAO
	SMSSession             map[int]*gate.SMS
	Stored                 chan struct{}
	TelegramNotification   chan *Notification
	NtfyNotification       chan *Notification
	KeypadReleased         bool
	NtfyURL                string
	NtfyToken              string
}

type Notification struct {
	msg    string
	system bool
	user   bool
}

type BTMacs struct {
	BLEAutoOpenLagMin int64
	BTMacSystem       map[string]string
	BTMacIgnore       map[string]string
	BTMacAutoOpenGate map[string]string
	BTMacNames        map[string]string
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
	UserId   string
	Sn       string
	Approved bool
	Type     int
	Tm       int64
	// 12: Time group restriction – date not allowed
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
	TimeGroupName       string
	TimeGroupId         string `json:"groupId"`
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

func PalesUserFromCsv(row []string, cols map[string]int) *PalesUser {
	//Phone number,First name,Last name,Admin,Linked device,Output 1,Time group,Remote control sn,Dial to open,Dial number (read only),Nearby only,Latch 1,Notes
	//79991234567 ,          ,         ,FALSE,FALSE        ,TRUE    ,          ,                 ,TRUE        ,                       ,FALSE      ,FALSE  ,

	u := new(PalesUser)
	u.Id = row[cols["Phone number"]]
	u.Firstname = row[cols["First name"]]
	u.Lastname = row[cols["Last name"]]
	u.DialToOpen = row[cols["Dial to open"]] == "TRUE"
	u.Admin = row[cols["Admin"]] == "TRUE"
	u.SecondaryDevice = row[cols["Linked device"]] == "TRUE"
	u.Output1 = row[cols["Output 1"]] == "TRUE"
	u.Output1Latch = row[cols["Latch 1"]] == "TRUE"
	u.LocalOnly = row[cols["Nearby only"]] == "TRUE"
	u.TimeGroupName = row[cols["Time group"]]
	i, ok := cols["TimeGroupId"]
	if ok {
		u.TimeGroupId = row[i]
	}
	return u
}

func (u *PalesUser) name() string {
	return fmt.Sprintf("%s %s %s", u.Id, u.Firstname, u.Lastname)
}

func (u *PalesUser) hasPlotNumber(n string) bool {
	pattern := `(?i)(^|[^a-zа-я0-9])` + regexp.QuoteMeta(n) + `([^a-zа-я0-9]|$)`
	re := regexp.MustCompile(pattern)
	return re.MatchString(u.Firstname + " " + u.Lastname)
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
	g.phoneCalls = make(chan *PhoneCall)
	g.phoneSmses = make(chan *PhoneSms)
	g.bleTrackings = make(chan *BLETracking, 8)
	g.openedEvets = make(chan OpenTime, 8)
	g.lastBLEOpenCommandTime = make(map[string]time.Time)
	g.bleTimes = make(map[string]time.Time)
	g.palesLastLog = &PalesLogUser{}
	g.lastOpenCommandTime = atomic.Int64{}
	g.lastOpenedTime = atomic.Int64{}
}

type PalEsTimeGroups struct {
	Msg    string
	Groups struct {
		List []*PalEsTimeGroup
	}
	groupMap map[string]*PalEsTimeGroup
}

func (tg *PalEsTimeGroups) init() {
	tg.groupMap = tg.toTimeGroupMap()
}

func (tg *PalEsTimeGroups) toTimeGroupMap() map[string]*PalEsTimeGroup {
	res := make(map[string]*PalEsTimeGroup)
	for _, g := range tg.Groups.List {
		g.init()
		res[g.Id] = g
	}
	return res
}

func (tg *PalEsTimeGroups) containsNow(groupId string, groupName string) bool {
	return tg.contains(groupId, groupName, time.Now())
}

func (tg *PalEsTimeGroups) contains(groupId string, groupName string, t time.Time) bool {
	g := tg.get(groupId, groupName)
	if g != nil {
		return g.contains(t)
	}
	// group not found, so have to use all time
	return true
}

func (tg *PalEsTimeGroups) get(groupId string, groupName string) *PalEsTimeGroup {
	if len(groupId) == 0 && len(groupName) == 0 {
		return nil
	}
	g := tg.groupMap[groupId]
	if g != nil {
		return g
	}
	for _, g := range tg.groupMap {
		if g.GroupName == groupName {
			return g
		}
	}
	return nil
}

type PalEsTimeGroup struct {
	Id        string `json:"_id"`
	GroupName string
	StartTime int
	EndTime   int
	Days      string
	StartDate int64
	EndDate   int64
	TimeArray []*PalEsTimeGroupDay
	daysArray []*PalEsTimeGroupDay
}

type PalEsTimeGroupDay struct {
	StartMinute int `json:"s"`
	EndMinute   int `json:"e"`
	DayOfWeek   int `json:"d"`
}

func (d *PalEsTimeGroupDay) contains(t time.Time) bool {
	minuteOfDay := t.Hour()*60 + t.Minute()
	return minuteOfDay >= d.StartMinute && minuteOfDay <= d.EndMinute
}

func (g *PalEsTimeGroup) init() {
	g.daysArray = make([]*PalEsTimeGroupDay, 7)
	for _, d := range g.TimeArray {
		if d.DayOfWeek >= 1 && d.DayOfWeek <= 7 {
			g.daysArray[d.DayOfWeek-1] = d
		}
	}
	for i, d := range g.daysArray {
		if d == nil {
			g.daysArray[i] = &PalEsTimeGroupDay{EndMinute: -1}
		}
	}
}

func (g *PalEsTimeGroup) contains(t time.Time) bool {
	unix := t.Unix()
	if unix < g.StartDate || unix > g.EndDate {
		return false
	}
	localTime := t.In(Location)
	d := g.daysArray[localTime.Weekday()]
	return d.contains(localTime)
}

func (g *Gate) handlingCalls(abort chan struct{}) {
	var gateTime time.Time
Loop:
	for {
		select {
		case call := <-g.phoneCalls:
			phone := strings.TrimPrefix(call.Phone, "+")
			name := phone
			u, ok := g.Phones[phone]
			if ok {
				name = u.name()
			}
			if call.CalledNumber == g.GateOpenNumber {
				if !ok {
					g.sendSystemNotification(fmt.Sprintf("%s %s uknown", call.timestamp(), phone))
					continue
				}
				if !g.allowed(phone) {
					g.sendSystemNotification(fmt.Sprintf("%s dial2 restricted %s", call.timestamp(), name))
					continue
				}
				if time.Since(gateTime) < 10*time.Second {
					g.sendSystemNotification(fmt.Sprintf("%s dial2 ok, opening already in action %s", call.timestamp(), name))
					continue
				}
				gateTime = time.Now()
				elapsed := time.Since(call.time())
				if elapsed > 20*time.Second {
					g.sendSystemNotification(fmt.Sprintf("%s dial2 ok, but call is overdue %d s  %s", call.timestamp(), elapsed/time.Second, name))
					continue
				}
				err := g.sendOpenCommandToGate(phone)
				if err != nil {
					g.sendSystemNotification(fmt.Sprintf("%s dial2 ok, %v  %s", call.timestamp(), name, err))
				} else {
					g.sendSystemNotification(fmt.Sprintf("%s dial2 ok  %s", call.timestamp(), name))
				}
				continue
			}
			if call.CalledNumber == g.GateInfoNumber {
				if !ok {
					g.sendUserNotification(fmt.Sprintf("%s не зарегистрирован", maskPhone(phone)))
					continue
				}
				if !g.allowed(phone) {
					g.sendUserNotification(fmt.Sprintf("%s проезд запрещен", maskPhone(phone)))
					continue
				}
				g.sendUserNotification(fmt.Sprintf("%s OK", maskPhone(phone)))
				continue
			}
		case <-abort:
			break Loop
		}
	}
}

func (g *Gate) allowed(phone string) bool {
	u, ok := g.Phones[phone]
	if !ok {
		return false
	}
	return (u.DialToOpen || u.LocalOnly) && !g.RestrictedPhones[phone] &&
		g.palEsTimeGroups.containsNow(u.TimeGroupId, u.TimeGroupName)
}

func (g *Gate) allowedKeypadCode(phone string) bool {
	u, ok := g.Phones[phone]
	if !ok {
		return false
	}
	gr := g.palEsTimeGroups.get(u.TimeGroupId, u.TimeGroupName)
	return (u.DialToOpen || u.LocalOnly) && !g.RestrictedPhones[phone] && gr == nil
}

func (g *Gate) handlingSmses(abort chan struct{}) {
Loop:
	for {
		select {
		case sms := <-g.phoneSmses:
			phone := strings.TrimPrefix(sms.Phone, "+")
			if len(phone) != 11 || !digitsRE.MatchString(phone) {
				continue
			}
			if sms.isTOTP() {
				secret, err := EncryptPhone(phone, time.Now())
				if err != nil {
					Logger.Errorf("Encrypt(%s, now): %v", phone, err)
					continue
				}
				m := gate.NewSMS(sms.Phone, time.Now().Add(24*time.Hour))
				m.Msg = fmt.Sprintf("https://7slavka.ru/totp/%s/ откройте ссылку, добавьте QR код в Google Authenticator", secret)
				err = g.SMSes.Insert(m)
				if err != nil {
					Logger.Errorf("save sms error: %v", err)
					continue
				}
				g.Stored <- struct{}{}
				Logger.Debugf("pending SMS: %s %q", m.Phone, m.Msg)

				continue
			}
			name := phone
			u, ok := g.Phones[phone]
			allowed := false
			if ok {
				allowed = g.allowedKeypadCode(phone)
				name = u.name()
			}
			if !ok {
				g.sendSystemNotification(fmt.Sprintf("%s uknown sender of SMS: %q", phone, sms.Sms))
				g.sendUserNotification(fmt.Sprintf("%s неизвестный номер SMS: %q", maskPhone(phone), sms.Sms))
				continue
			}
			if !allowed {
				g.sendSystemNotification(fmt.Sprintf("%s restricted sender of SMS: %q", name, sms.Sms))
				g.sendUserNotification(fmt.Sprintf("%s нет разрешения на проезд SMS: %q", maskPhone(phone), sms.Sms))
				continue
			}
			if sms.isTempCode() {
				g.KeypadCodesRequests <- sms
				continue
			}
			g.sendSystemNotification(fmt.Sprintf("unknown sms format. sent: %s by: %s: text: %q", sms.timestampSent(), name, sms.Sms))
			g.sendUserNotification(fmt.Sprintf("Неизвестный формат. Отправлено: %s номер: %s: SMS: %q", sms.timestampSent(), maskPhone(phone), sms.Sms))

		case <-abort:
			break Loop
		}
	}
}

func (g *Gate) readingSMSesForSend(abort chan struct{}) {
	ticker := time.NewTicker(20 * time.Second)
Loop:
	for {
		select {
		case <-ticker.C:
			if len(g.PendingSMSes) > 1 {
				continue
			}
			g.loadSMSes()

		case <-g.Stored:
			if len(g.PendingSMSes) > 1 {
				continue
			}
			g.loadSMSes()

		case <-abort:
			break Loop
		}
	}
}

func (g *Gate) loadSMSes() {
	smses, err := g.SMSes.ListNew(min(10, cap(g.PendingSMSes)-10))
	if err != nil {
		Logger.Errorf("error reading smses %v", err)
		return
	}
	sess := make(map[int]*gate.SMS, len(smses))
	cnt := 0
	for _, m := range smses {
		sess[m.ID] = &m
		if _, ok := g.SMSSession[m.ID]; ok {
			continue
		}
		g.PendingSMSes <- &m
		cnt++
	}
	g.SMSSession = sess
	Logger.Debugf("read: %d, added to channel: %d, pending: %d sms", len(smses), cnt, len(g.PendingSMSes))
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

func (g *Gate) sendSystemNotification(msg string) {
	g.sendNotification(msg, true, false)
}

func (g *Gate) sendUserNotification(msg string) {
	g.sendNotification(msg, false, true)
}

func (g *Gate) sendNotification(msg string, system, user bool) {
	if system {
		g.TelegramNotification <- &Notification{msg: msg, system: system, user: user}
		g.NtfyNotification <- &Notification{msg: msg, system: system, user: user}
	}
	if user {
		g.NtfyNotification <- &Notification{msg: msg, system: system, user: user}
	}
}

func (g *Gate) sendOpenCommandToGate(text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	url := fmt.Sprintf(g.GateUrl, url.QueryEscape(text))
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(""))
	if err != nil {
		return err
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(g.User, g.Password)

	now := time.Now()
	g.lastOpenCommandTime.Store(now.Unix())

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		Logger.Errorf("error calling gate %q: %v", url, err)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		Logger.Errorf("error calling gate %q: %d", url, resp.StatusCode)
		return fmt.Errorf("http %d", resp.StatusCode)
	}
	g.updateLastOpenedTime(now)
	Logger.Debugf("%q http %d", text, resp.StatusCode)
	return err
}

func (g *Gate) handlingBLETracking(abort chan struct{}) {
	var firstWaitIsOver <-chan time.Time
	var nextWaitIsOver <-chan time.Time
	const firstDuration = 3 * time.Second
	const nextDuration = 60 * time.Second
	ticker := time.NewTicker(firstDuration)
	var tt []*BLETracking
	var systemLocation int
Loop:
	for {
		select {
		case t := <-g.bleTrackings:
			// ignore if system location is unknown or not from system location
			if systemLocation != 0 && t.Location != systemLocation {
				continue
			}
			if _, ok := g.BTMacs.BTMacSystem[t.MAC]; ok {
				if systemLocation == 0 && t.Location != 0 {
					systemLocation = t.Location
					Logger.Debugf("BLE: system location = %d", systemLocation)
				}
				continue
			}
			if _, ok := g.BTMacs.BTMacIgnore[t.MAC]; ok {
				continue
			}
			g.checkAndOpenOnBT(t)
			if _, ok := g.BTMacs.BTMacNames[t.MAC]; ok {
				tt = append(tt, t)
			}
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

		case t := <-g.openedEvets:
			//g.openCommandTime.Store(time.Now().Unix())
			if time.Since(g.lastOpenedNotification) < 71*time.Second {
				Logger.Debugf("gate opened %s", t.timestampSent())
				continue
			}
			now := time.Now()
			g.lastOpenedNotification = now
			g.updateLastOpenedTime(now)
			Logger.Infof("gate opened %s", t.timestampSent())
			g.sendSystemNotification(fmt.Sprintf("gate opened %s", t.timestampSent()))

		case <-abort:
			break Loop
		}
	}
}

func (g *Gate) updateLastOpenedTime(now time.Time) {
	for {
		nanos := g.lastOpenedTime.Load()
		if now.UnixNano() <= nanos {
			break
		}
		if g.lastOpenedTime.CompareAndSwap(nanos, now.UnixNano()) {
			break
		}
	}
}

func (g *Gate) handlingKeypadRequests(abort chan struct{}) {
	codes := make(map[string]*gate.KeypadCode)
	cc, err := g.KeypadCodes.ListActive()
	if err != nil {
		Logger.Errorf("error reading db kpcodes: %v", err)
	}
	for _, c := range cc {
		codes[c.Code] = &c
	}
Loop:
	for {
		select {
		case sms := <-g.KeypadCodesRequests:
			msg := sms.Sms
			now := time.Now()
			if sms.isTempCode() {
				min := 100000
				max := 999999
				if msg == "30m" {
					min = 10000
					max = 99999
				}
				code := ""
				for {
					n := rand.IntN(max-min+1) + min
					code = strconv.Itoa(n)
					if _, ok := codes[code]; !ok {
						break
					}
				}
				m := gate.NewSMS(sms.Phone, now.Add(20*time.Minute))
				kpCode := &gate.KeypadCode{Code: code, RequesterPhone: sms.Phone, CreatedTimeMilli: time.Now().UnixMilli()}
				hours := sms.tempCodeTTLHours()
				if hours != 0 {
					kpCode.TTLMinutes = hours * 60
					m.Msg = fmt.Sprintf("код для шлагбаума %s. действителен %d ч с первого ввода", code, hours)
				} else {
					kpCode.EndTimeMilli = now.Add(30 * time.Minute).UnixMilli()
					m.Msg = fmt.Sprintf("код для шлагбаума %s. действителен 30 мин", code)
				}
				err := g.KeypadCodes.Insert(kpCode)
				if err != nil {
					Logger.Errorf("error saving to db kpcodes: %v", err)
					continue
				}
				err = g.SMSes.Insert(m)
				if err != nil {
					Logger.Errorf("error saving to db kpcodes: %v", err)
					continue
				}
				g.Stored <- struct{}{}
				Logger.Debugf("pending SMS: %s %q", m.Phone, m.Msg)
				continue
			}
		case <-abort:
			break Loop
		}
	}

}

func (g *Gate) checkAndOpenOnBT(bt *BLETracking) {
	phone, ok := g.BTMacs.BTMacAutoOpenGate[bt.MAC]
	if !ok {
		return
	}
	u, ok := g.Phones[phone]
	if !ok {
		return
	}
	if time.Since(g.lastOpenedNotification) < 71*time.Second {
		return
	}
	// open gate and telegram message lag
	if time.Since(g.lastBLEOpenCommandTime[bt.MAC]) < time.Duration(g.BTMacs.BLEAutoOpenLagMin)*time.Minute {
		return
	}
	g.lastBLEOpenCommandTime[bt.MAC] = time.Now()
	lastMacTime := g.bleTimes[bt.MAC]
	t := time.Unix(bt.Time, 0)
	g.bleTimes[bt.MAC] = t
	// avoid nuisance gate cycling while in range
	if t.Sub(lastMacTime) <= g.BLEPeriodSec*time.Second {
		return
	}
	if !g.allowed(phone) {
		g.sendSystemNotification(fmt.Sprintf("%s BLE restricted %s", bt.timestamp(), u.name()))
		return
	}
	err := g.sendOpenCommandToGate(fmt.Sprintf("%s %s", bt.MAC, phone))
	if err != nil {
		g.sendSystemNotification(fmt.Sprintf("%s BLE:%s ok, %v  %s", bt.timestamp(), bt.MAC, u.name(), err))
	} else {
		g.sendSystemNotification(fmt.Sprintf("%s BLE:%s ok  %s", bt.timestamp(), bt.MAC, u.name()))
	}
}

func (g *Gate) sendToTelegramMsg(tt []*BLETracking) {
	if len(tt) == 0 {
		return
	}
	var msg strings.Builder
	for _, t := range tt {
		mac := g.BTMacs.BTMacNames[t.MAC]
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
		if len(t.UUID) != 0 {
			msg.WriteString(" UUID:")
			msg.WriteString(t.UUID)
		}
		if t.CompanyId != 0 {
			msg.WriteString(" CompanyId:")
			msg.WriteString(strconv.Itoa(t.CompanyId))
		}
		if t.Time != 0 {
			msg.WriteString(" time:")
			msg.WriteString(t.timestamp())
		}
		msg.WriteString("\n")
	}
	g.sendSystemNotification(msg.String())
}

func (g *Gate) palesLoginAndLoadLoop(abort chan struct{}) {
	g.login(false)
	{
		st := g.loadPalesTimeGroups()
		if st == http.StatusUnauthorized {
			g.login(true)
			g.loadPalesTimeGroups()
		}
	}
	g.loadPalesUsers()

	g.palesLastLog.Tm = time.Now().Unix()
	g.loadPalesLogs(20 * time.Second)

	minuteLoginTicker := time.NewTicker(time.Minute)
	minuteLogsTicker := time.NewTicker(time.Minute)
	thirtyMinuteTicker := time.NewTicker(30 * time.Minute)
	dailyTicker := time.NewTicker(24 * time.Hour)
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
		case <-dailyTicker.C:
			g.loadPalesTimeGroups()
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
			g.palesLastLog.Tm), nil)

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
	maxLog := result.Log.List[0]
	slices.SortFunc(result.Log.List, func(a, b *PalesLogUser) int {
		return cmp.Compare(a.Tm, b.Tm)
	})
	for _, l := range result.Log.List {
		if l.Tm > maxLog.Tm {
			maxLog = l
		}
		if l.Approved {
			g.updateLastOpenedTime(time.Unix(l.Tm, 0))
		}
		if g.palesLastLog.UserId == l.UserId && g.palesLastLog.Tm == l.Tm && g.palesLastLog.Type == l.Type {
			continue
		}
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
	g.palesLastLog = maxLog
	Logger.Infof("received %d pal-es log records", len(result.Log.List))
	g.sendSystemNotification(msg.String())
	return resp.StatusCode
}

func (g *Gate) loadPalesTimeGroups() int {
	if g.palEsTimeGroups == nil {
		var tg PalEsTimeGroups
		tg.init()
		g.palEsTimeGroups = &tg
	}
	if len(g.PalesPortalUserToken) == 0 {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", "https://portal.pal-es.com/api1/device/4G600211776/groups", nil)

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
		Logger.Infof("pal-es timegroups http %d", resp.StatusCode)
		return resp.StatusCode
	}
	Logger.Debugf("pal-es timegroups http %d", resp.StatusCode)
	var result PalEsTimeGroups
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		Logger.Errorf("error unmarshalling pal-es log http response: %v", err)
		return -1
	}
	result.init()
	g.palEsTimeGroups = &result
	Logger.Debugf("pal-es timegroups %s, %d", result.Msg, len(result.Groups.List))
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
		g.Phones[u.Id] = u
	}
	var records [][]string
	records = append(records, []string{
		"Phone number", "First name", "Last name",
		"Admin", "Linked device", "Output 1",
		"Time group", "Remote control sn", "Dial to open",
		"Dial number (read only)", "Nearby only", "Latch 1",
		"Notes", "TimeGroupId"})
	for _, u := range result.Users.List {
		records = append(records, []string{
			u.Id, u.Firstname, u.Lastname,
			If(u.Admin, "TRUE", "FALSE"), If(u.SecondaryDevice, "TRUE", "FALSE"), If(u.Output1, "TRUE", "FALSE"),
			u.TimeGroupName, "", If(u.DialToOpen, "TRUE", "FALSE"),
			"FALSE", "FALSE", If(u.Output1Latch, "TRUE", "FALSE"),
			"", u.TimeGroupId})
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

var (
	Err400BadFormat       = errors.New("bad format")
	Err403Forbidden       = errors.New("forbidden")
	Err429TooManyRequests = errors.New("too many requests")
)

func (g *Gate) keypadCode(c KeypadCode) error {
	Logger.Infof("keypad code %s  %s", c.Code, c.timestampSent())
	t := time.Unix(c.Time, 0)
	if !g.RateWatcher.hit(t) {
		g.sendSystemNotification(fmt.Sprintf("keypad code %s TOO MANY REQUESTS %s ", c.Code, c.timestampSent()))
		g.sendUserNotification(fmt.Sprintf("слишком много попыток ввода кода. подождите несколько минут %s ", c.timestampSent()))
		return Err429TooManyRequests
	}
	n := len(c.Code)
	if n <= 5 && strings.HasPrefix(c.Code, "0") {
		if c.Code == "0000" {
			g.sendUserNotification(fmt.Sprintf(`неизвесный гость ввел код %s "я приехал"`, c.Code))
			return nil
		}
		if strings.HasPrefix(c.Code, "00") && n >= 3 && n <= 5 {
			plotN, err := strconv.Atoi(c.Code)
			if err == nil && plotN >= 1 && plotN <= 315 {
				g.sendUserNotification(fmt.Sprintf("ввели код %s - гости %d участка запрашивают проезд", c.Code, plotN))
				return nil
			}
		}
		g.sendUserNotification(fmt.Sprintf("неизвестный код %s", c.Code))
		return Err400BadFormat

	}
	if n == 5 {
		code, err := gate.Find(g.KeypadCodes, c.Code)
		if err != nil {
			Logger.Errorf("error finding kpcode %v", err)
			return Err400BadFormat
		}
		if code == nil {
			g.sendUserNotification(fmt.Sprintf("код %s не найден или уже закончил свое действие", c.Code))
			return Err400BadFormat
		}
		if code.EndTimeMilli == 0 {
			code.EndTimeMilli = time.Now().Add(time.Duration(code.TTLMinutes) * time.Minute).UnixMilli()
			g.KeypadCodes.Update(code)
		}
		if g.KeypadReleased {
			err = g.sendOpenCommandToGate(fmt.Sprintf("keypad %s", code.Code))
		}
		if err != nil {
			g.sendSystemNotification(fmt.Sprintf("keypad code OK %s, open command error %v", code.Code, err))
		} else {
			g.sendSystemNotification(fmt.Sprintf("keypad code OK %s, sent open command", code.Code))
			if code.Temporal() {
				g.sendUserNotification(fmt.Sprintf("гость %s успешно ввел код", maskPhone(code.RequesterPhone)))
			}
		}
		return err
	}
	if len(c.Code) != 9 {
		g.sendSystemNotification(fmt.Sprintf("keypad code %s  %s", c.Code, c.timestampSent()))
		return Err400BadFormat
	}
	totpCode := c.Code[len(c.Code)-6:]
	phonePostfix := c.Code[:len(c.Code)-6]
	phone := g.findTOTPPhoneByCode(phonePostfix, totpCode)
	if phone == "" {
		return Err403Forbidden
	}
	u, ok := g.Phones[phone]
	if !ok {
		g.sendSystemNotification(fmt.Sprintf("keypad code %s is valid totp code for %s, but phone is not found in gate register", c.Code, phone))
		return Err403Forbidden
	}
	info := fmt.Sprintf("%s %s %s", c.Code, u.Firstname, u.Lastname)
	if !g.allowed(phone) {
		g.sendSystemNotification(fmt.Sprintf("keypad code: user restricted %s", info))
		return Err403Forbidden
	}
	err := g.sendOpenCommandToGate(fmt.Sprintf("keypad %s", c.Code))
	if err != nil {
		g.sendSystemNotification(fmt.Sprintf("keypad code %s  %v", info, err))
	} else {
		g.sendSystemNotification(fmt.Sprintf("keypad code OK %s", info))
	}
	return err
}

func (g *Gate) findTOTPPhoneByCode(phonePostfix string, totpCode string) string {
	totpPhones, err := g.TOTPPhones.ListEndsWith(phonePostfix)
	if err != nil {
		Logger.Errorf("error finding totp phone %v", err)
		return ""
	}
	for _, p := range totpPhones {
		phone := p.Phone
		valid, err := validateTOTPCodeForPhone(phone, totpCode)
		if err != nil {
			Logger.Errorf("totp validation error %v", err)
			continue
		}
		if valid {
			return p.Phone
		}
	}
	return ""
}

func validateTOTPCodeForPhone(phone string, totpCode string) (bool, error) {
	secret := totpSecret(phone)
	secretBase32 := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte(secret))

	valid, err := totp.ValidateCustom(totpCode, secretBase32, time.Now(), totp.ValidateOpts{
		Period:    30, // стандарт для Google Authenticator
		Skew:      1,  // позволяет код из прошлого или следующего 30-секундного интервала
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	return valid, err
}

func totpSecret(phone string) string {
	salt := "SNT_Semislavka"
	h := sha1.New()
	h.Write([]byte(phone + salt))
	hashBytes := h.Sum(nil)
	hashStr := hex.EncodeToString(hashBytes)
	secret := hashStr[:16]
	return secret
}

func (g *Gate) sendingSystemNotification(abort chan struct{}) {
Loop:
	for {
		select {
		case m := <-g.TelegramNotification:
			msg := m.msg
			Logger.Infof("telegram: %s", msg)
			g.sendTelegram(msg)

		case <-abort:
			break Loop
		}
	}
}

func (g *Gate) sendTelegram(msg string) {
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

func (g *Gate) sendingUserNotification(abort chan struct{}) {
	server, _ := url.Parse(g.NtfyURL)
	publisher, err := gotfy.NewPublisher(server, gotfy.WithAuth("", g.NtfyToken))
	if err != nil {
		Logger.Errorf("error NewPublisher: %v", err)
	}
Loop:
	for {
		select {
		case m := <-g.NtfyNotification:
			msg := m.msg
			if publisher == nil {
				Logger.Debugf("CAN'T SEND TO ntfy DUE TO PREVIOUS ERROR: %s", msg)
				continue
			}
			if m.user {
				g.sendNtfy(publisher, "7g-events", msg)
			}
			if m.system {
				g.sendNtfy(publisher, "system", msg)
			}

		case <-abort:
			break Loop
		}
	}
}

func (g *Gate) sendNtfy(publisher *gotfy.Publisher, topic, msg string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(g.TelegramTimeoutSec)*time.Second)
	defer cancel()
	_, err := publisher.SendMessage(ctx, &gotfy.Message{
		Topic:   topic,
		Message: msg,
	})
	if err != nil {
		Logger.Errorf("ntfy: %s  error: %v", msg, err)
	} else {
		Logger.Infof("ntfy: %s", msg)
	}
}

type HitCounter struct {
	N      int
	buffer []time.Time
	mu     sync.Mutex // guards hits and buffer
	hits   []time.Time
	end    int
}

func (c *HitCounter) init() {
	sz := max(16, c.N*2)
	c.buffer = make([]time.Time, sz, sz)
	c.hits = c.buffer[:0]
}

func (c *HitCounter) hit(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.end == len(c.buffer) {
		c.end = c.N
		copy(c.buffer, c.hits[1:])
		c.buffer[c.N-1] = t
		c.hits = c.buffer[:c.N]
		return
	}
	c.end++
	c.hits = append(c.hits, t)
	if len(c.hits) > c.N {
		c.hits = c.hits[1:]
	}
}

func (c *HitCounter) count(d time.Duration) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.hits) == 0 || d <= 0 {
		return 0
	}
	res := 1
	if len(c.hits) == 1 {
		return res
	}
	begin := c.hits[len(c.hits)-1].Add(-d)
	for i := len(c.hits) - 2; i >= 0; i-- {
		if c.hits[i].Before(begin) {
			return res
		}
		res++
	}
	return res
}

func (c *HitCounter) till(t time.Time) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	var last time.Time
	if len(c.hits) != 0 {
		last = c.hits[len(c.hits)-1]
	}
	return t.Sub(last)
}

type RateWatcher struct {
	Duration         time.Duration
	ThrottleDuration time.Duration
	hitCounter       *HitCounter
}

func (w *RateWatcher) Init(limit int) {
	w.hitCounter = &HitCounter{N: limit}
	w.hitCounter.init()
}

func (w *RateWatcher) hit(t time.Time) bool {
	if w.hitCounter.till(t) < w.ThrottleDuration && w.hitCounter.count(w.Duration) >= w.hitCounter.N {
		return false
	}
	w.hitCounter.hit(t)
	return w.hitCounter.count(w.Duration) < w.hitCounter.N
}

type AutomateReq struct {
	TimeMilli  float64 `json:"time"`
	HostNumber string  `json:"host_number"`
}

func (r *AutomateReq) time() time.Time {
	return time.UnixMilli(int64(math.Round(r.TimeMilli * 1000)))
}

type AutomateSMS struct {
	Phone string `json:"phone"`
	Text  string `json:"text"`
}

// Ключ должен быть 16, 24 или 32 байта (AES-128, 192, 256)

var key = []byte("k9wkKLJqpa_lJl-l")
var april2026 = time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)

func EncryptPhone(phone string, tm time.Time) (string, error) {
	if len(phone) > 9 {
		phone = phone[len(phone)-9:]
	}
	hours := tm.Sub(april2026) / time.Hour
	return encrypt(phone + strconv.Itoa(int(hours)))
}

func encrypt(digits string) (string, error) {
	num, err := strconv.ParseUint(digits, 10, 64)
	if err != nil {
		return "", err
	}
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, num)

	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	nonce := make([]byte, gcm.NonceSize())
	io.ReadFull(crand.Reader, nonce)

	ciphertext := gcm.Seal(nonce, nonce, buf, nil)

	return base64.RawURLEncoding.EncodeToString(ciphertext), nil
}

func DecryptPhone(cryptoText string) (string, time.Time, error) {
	s, err := decrypt(cryptoText)
	if err != nil {
		return s, time.Time{}, err
	}
	months, err := strconv.Atoi(s[9:])
	return "79" + s[:9], april2026.Add(time.Duration(months) * time.Hour), err
}

func decrypt(cryptoText string) (string, error) {
	data, err := base64.RawURLEncoding.DecodeString(cryptoText)
	if err != nil {
		return "", err
	}
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)

	nonceSize := gcm.NonceSize()
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}

	// Конвертируем байты обратно в число, а число в строку
	num := binary.BigEndian.Uint64(plaintext)
	return strconv.Itoa(int(num)), nil // %016d сохранит ведущие нули
}
