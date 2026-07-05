package tgsrv

import (
	"7stgbot/config"
	"7stgbot/gate"
	"bytes"
	"cmp"
	"context"
	"crypto/aes"
	"crypto/cipher"
	crand "crypto/rand"
	"crypto/sha1"
	"database/sql"
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

const (
	gateInOpenStateKey = "g.inOpenedState.b"
	lastOpenedTimeKey  = "g.lastOpenedTime.i"
	scheduleKey        = "g.schedule"
	bleScheduleKey     = "g.bleSchedule"
	badKeysKey         = "g.badKeys"
	minCodeLenKey      = "g.minCodeLen.i"
)

type GateCommand int

const (
	Open GateCommand = iota
	KeepOpenBegin
	KeepOpenEnd
	Lock
	OpenedEvent
)

type GateCommandAndText struct {
	command            GateCommand
	text               string
	systemNotification string
	args               any
}

type Gate struct {
	Phones                 map[string]*PalESUser
	RestrictedPhones       map[string]bool
	Cfg                    *config.Config
	TelegramUrl            string
	TelegramChatId         string
	TelegramTimeoutSec     int
	ProxyUrl               string
	phoneCalls             chan *PhoneCall
	phoneSmses             chan *PhoneSms
	bleTrackings           chan []*BLETracking
	wifiClients            chan any
	openedEvets            chan OpenTime
	PalesPortalUser        string
	PalesPortalPwd         string
	PalesTokenFilename     string
	PalESPortalUserToken   atomic.Value
	CfgDir                 string
	palesLastLog           *PalesLogUser
	lastOpenedNotification time.Time
	lastOpenCommandTime    atomic.Int64
	lastOpenedTime         atomic.Int64
	GateOpenNumber         string
	GateInfoNumber         string
	palEsTimeGroups        *PalEsTimeGroups
	RateWatcher            *RateWatcher
	PendingCalls           chan *gate.Call
	PendingSMSes           chan *gate.SMS
	KeypadCodesRequests    chan *PhoneSms
	SMSes                  gate.SMSesDAO
	KeypadCodes            gate.KeypadCodesDAO
	TOTPPhones             gate.TOTPPhonesDAO
	MattermostUsers        gate.MattermostUsersDAO
	Settings               gate.SettingsDAO
	SMSSession             map[int]*gate.SMS
	Stored                 chan struct{}
	TelegramNotification   chan *Notification
	GateCommands           chan *GateCommandAndText
	NtfyNotification       chan *Notification
	NtfyURL                string
	NtfyToken              string
	schedule               chan map[string]int
	bleSchedule            chan map[string]int
}

type Notification struct {
	msg    string
	system bool
	user   bool
}

type PalesLoginResp struct {
	Msg  string
	User struct {
		Token string
	}
}

type PalESLog struct {
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
	Type     int // see typeName()
	Tm       int64
	/*
	   22 - Duplicated remote
	   16 - Latch active
	   12 - Time group restriction – date not allowed

	   1 - Not in list (Nearby Only for dials too)
	   3 - No response
	   4 - User smartphone internet problem
	   61 - Bluetooth repeated id
	*/
	Reason    int
	Firstname string
	Lastname  string
}

func (l *PalesLogUser) Time() time.Time {
	return time.Unix(l.Tm, 0)
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

func (u *PalesLogUser) isInet() bool {
	return u.Type == 100
}

func (u *PalesLogUser) isRemote() bool {
	return u.Type == 2
}

func (u *PalesLogUser) isWeakReason() bool {
	switch u.Reason {
	case 1, 3, 4, 61:
		return true
	}
	return false
}

func (u *PalesLogUser) Phone() string {
	if u.isRemote() {
		return u.UserId
	}
	switch len(u.UserId) {
	case 10:
		return "7" + u.UserId
	case 9:
		return "79" + u.UserId
	case 0:
		switch len(u.Sn) {
		case 11:
			return u.Sn
		case 10:
			return "7" + u.Sn
		case 9:
			return "79" + u.Sn
		}
	}
	return u.UserId
}

type PalesUsers struct {
	Msg   string
	Users struct {
		Count int
		List  []*PalESUser
	}
}

type PalESUser struct {
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

func (u *PalESUser) name() string {
	return fmt.Sprintf("%s %s %s", u.Id, u.Firstname, u.Lastname)
}

func (u *PalESUser) hasPlotNumber(n string) bool {
	pattern := `(?i)(^|[^a-zа-я0-9])` + regexp.QuoteMeta(n) + `([^a-zа-я0-9]|$)`
	re := regexp.MustCompile(pattern)
	return re.MatchString(u.Firstname + " " + u.Lastname)
}

type ESPHomeRelayResp struct {
	NameID string `json:"name_id"`
	ID     string
	Value  bool
	State  string
}

type ESPHomeTextResp struct {
	Value string
}

func PalesUserFromCsv(row []string, cols map[string]int) *PalESUser {
	//Phone number,First name,Last name,Admin,Linked device,Output 1,Time group,Remote control sn,Dial to open,Dial number (read only),Nearby only,Latch 1,Notes
	//79991234567 ,          ,         ,FALSE,FALSE        ,TRUE    ,          ,                 ,TRUE        ,                       ,FALSE      ,FALSE  ,

	u := new(PalESUser)
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

func (g *Gate) Init(cfg *config.Config, db *sql.DB) {
	g.Cfg = cfg
	g.TelegramUrl = cfg.TelegramUrl
	g.TelegramChatId = cfg.TelegramChatId
	g.TelegramTimeoutSec = cfg.TelegramTimeoutSec
	g.ProxyUrl = cfg.ProxyUrl
	g.PalesPortalUser = cfg.PalesPortalUser
	g.PalesPortalPwd = cfg.PalesPortalPwd
	g.GateOpenNumber = cfg.GateOpenNumber
	g.GateInfoNumber = cfg.GateInfoNumber
	g.NtfyURL = cfg.NtfyURL
	g.NtfyToken = cfg.NtfyToken
	g.RateWatcher = &RateWatcher{
		Duration:         time.Duration(cfg.KeypadHitLimitDurationMinutes) * time.Minute,
		ThrottleDuration: time.Duration(cfg.KeypadThrottleMinutes) * time.Minute}
	g.RateWatcher.Init(cfg.KeypadHitLimit)
	g.PendingCalls = make(chan *gate.Call, 32)
	g.PendingSMSes = make(chan *gate.SMS, 32)
	g.SMSSession = make(map[int]*gate.SMS)
	g.SMSes = gate.NewSMSes(db)
	g.KeypadCodes = gate.NewKeypadCodes(db)
	g.TOTPPhones = gate.NewTOTPPhones(db)
	g.MattermostUsers = gate.NewMattermostUsers(db)
	g.Settings = gate.NewSettings(db)
	g.Stored = make(chan struct{}, 8)
	g.TelegramNotification = make(chan *Notification, 128)
	g.GateCommands = make(chan *GateCommandAndText, 4)
	g.NtfyNotification = make(chan *Notification, 128)
	g.KeypadCodesRequests = make(chan *PhoneSms, 32)
	g.phoneCalls = make(chan *PhoneCall)
	g.phoneSmses = make(chan *PhoneSms)
	g.bleTrackings = make(chan []*BLETracking, 8)
	g.wifiClients = make(chan any, 8)
	g.openedEvets = make(chan OpenTime, 8)
	g.palesLastLog = &PalesLogUser{}
	g.lastOpenCommandTime = atomic.Int64{}
	g.lastOpenedTime = atomic.Int64{}
	{
		s, err := g.Settings.Find(lastOpenedTimeKey)
		now := time.Now().Unix()
		if err != nil {
			g.lastOpenedTime.Store(now)
		} else {
			g.lastOpenedTime.Store(int64(s.ValueInt(int(now))))
		}
	}
	g.schedule = make(chan map[string]int, 1)
	g.bleSchedule = make(chan map[string]int, 1)
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

func NewOpenSchedule(p map[string]int) *OpenSchedule {
	if len(p) == 0 {
		return NewOpenSchedule(map[string]int{"00:00": 55500000})
	}
	time := make([]string, 0, 4)
	for k := range p {
		time = append(time, k)
	}
	slices.Sort(time)
	n := len(time)
	minutes := make([]int, n)
	for i, t := range time {
		minutes[i] = p[t]
	}
	return &OpenSchedule{time: time, minutes: minutes}
}

type OpenSchedule struct {
	time    []string
	minutes []int
}

func (s *OpenSchedule) period(p time.Time) int {
	time := p.In(Location).Format("15:04")
	if len(time) < 5 {
		time = "0" + time
	}
	for i, t := range s.time {
		if time < t {
			if i == 0 {
				break
			}
			return s.minutes[i-1]
		}
	}
	return s.minutes[len(s.minutes)-1]
}

func (g *Gate) handlingCalls(abort chan struct{}) {
	var gateTime time.Time
Loop:
	for {
		select {
		case call := <-g.phoneCalls:
			phone := strings.TrimPrefix(call.Phone, "+")
			_, ok := g.Phones[phone]
			if call.CalledNumber == g.GateOpenNumber {
				if !ok {
					g.sendSystemNotification(fmt.Sprintf("%s %s uknown", call.timestamp(), phone))
					g.sendSMS(phone, "Ваш номер не зарегистрирован в реестре шлагбаума. Обратитесь в правление.",
						time.Now().Add(24*time.Hour))
					continue
				}
				if !g.allowedNow(phone) {
					g.sendSystemNotification(fmt.Sprintf("%s dial2 restricted %s %s", call.timestamp(), phone, g.userName(phone, "")))
					continue
				}
				if time.Since(gateTime) < 10*time.Second {
					g.sendSystemNotification(fmt.Sprintf("%s dial2 ok, opening already in action %s %s", call.timestamp(), phone, g.userName(phone, "")))
					continue
				}
				gateTime = time.Now()
				elapsed := time.Since(call.time())
				if elapsed > 20*time.Second {
					g.sendSystemNotification(fmt.Sprintf("%s dial2 ok, but call is overdue %d s  %s %s", call.timestamp(), elapsed/time.Second, phone, g.userName(phone, "")))
					continue
				}
				g.openGate(phone, "")
				g.sendSystemNotification(fmt.Sprintf("OPENED %s dial2  %s %s", call.timestamp(), phone, g.userName(phone, "")))
				continue
			}
			if call.CalledNumber == g.GateInfoNumber {
				if !ok {
					g.sendUserNotification(fmt.Sprintf("%s не зарегистрирован", maskPhone(phone)))
					continue
				}
				if !g.allowedNow(phone) {
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

func (g *Gate) allowedNow(phone string) bool {
	u, ok := g.Phones[phone]
	if !ok {
		return false
	}
	return (u.DialToOpen || u.LocalOnly) && !g.RestrictedPhones[phone] &&
		g.palEsTimeGroups.containsNow(u.TimeGroupId, u.TimeGroupName)
}

func (g *Gate) allowedAllTime(phone string) bool {
	u, ok := g.Phones[phone]
	if !ok {
		return false
	}
	return (u.DialToOpen || u.LocalOnly) && !g.RestrictedPhones[phone] &&
		g.palEsTimeGroups.get(u.TimeGroupId, u.TimeGroupName) == nil
}

func (g *Gate) userName(phone, defaultName string) string {
	u, ok := g.Phones[phone]
	if !ok {
		return defaultName
	}
	return u.name()
}

var digitsRE = regexp.MustCompile(`^[0-9]+$`)

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
				msg := fmt.Sprintf("https://7slavka.ru/totp/%s/ откройте ссылку, добавьте QR код в Google Authenticator", secret)
				g.sendSMS(sms.Phone, msg, time.Now().Add(24*time.Hour))
				continue
			}
			name := phone
			u, ok := g.Phones[phone]
			allowed := false
			if ok {
				allowed = g.allowedAllTime(phone)
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

func (g *Gate) sendSMS(phone string, msg string, deadline time.Time) {
	m := gate.NewSMS(phone, deadline)
	m.Msg = msg
	err := g.SMSes.Insert(m)
	if err != nil {
		return
	}
	g.Stored <- struct{}{}
	Logger.Debugf("pending SMS: %s %q", m.Phone, m.Msg)
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
		select {
		case g.TelegramNotification <- &Notification{msg: msg, system: system, user: user}:
		default:
			Logger.Warn("channel overflow - telegram: %s", msg)
		}
	}
	if system || user {
		select {
		case g.NtfyNotification <- &Notification{msg: msg, system: system, user: user}:
		default:
			Logger.Warn("channel overflow - ntfy: %s", msg)
		}
	}
}

func (g *Gate) openGate(text, systemNotification string) {
	g.GateCommands <- &GateCommandAndText{command: Open, text: text, systemNotification: systemNotification}
}

func (g *Gate) keepOpenGate() {
	g.GateCommands <- &GateCommandAndText{command: KeepOpenBegin}
}

func (g *Gate) endKeepOpenGate() {
	g.GateCommands <- &GateCommandAndText{command: KeepOpenEnd}
}

func (g *Gate) lock(d time.Duration) {
	g.GateCommands <- &GateCommandAndText{command: Lock, args: d}
}

type OpenMonitor struct {
	secondToLast time.Time
	last         time.Time
	doubling     bool
}

func (m *OpenMonitor) opened(t time.Time) {
	if t.Before(m.secondToLast) {
		return
	}
	if t.Before(m.last) {
		m.secondToLast = t
		dist := m.last.Sub(m.secondToLast)
		m.doubling = dist >= 7*time.Second && dist < 17*time.Second
	} else {
		m.secondToLast = m.last
		m.last = t
		if m.doubling {
			m.doubling = false
		} else {
			dist := m.last.Sub(m.secondToLast)
			m.doubling = dist >= 7*time.Second && dist < 17*time.Second
		}
	}
}

func (m *OpenMonitor) isDoublingBefore(before time.Time) bool {
	return m.doubling && m.last.Before(before)
}

/*
GET /switch/Main%20Relay -> {"name_id":"switch/Main Relay","id":"switch-main_relay","value":true,"state":"ON"} | ..."value":false,"state":"OFF"}
direct: POST /switch/Main%20Relay/turn_on
direct: POST /switch/Main%20Relay/turn_off
indirect: POST /text/Relay_Turn_On/set?value=text
indirect: POST /text/Relay_Turn_Off/set?value=text
*/
func (g *Gate) handlingGateState(abort <-chan struct{}, cfg *config.Config, cfgSub chan *config.Config) {
	inOpenedState := false
	{
		s, err := g.Settings.Find(gateInOpenStateKey)
		if err != nil {
			Logger.Errorf("read setting %q error: %v", gateInOpenStateKey, err)
		} else {
			inOpenedState = s.ValueBool(false)
		}
	}
	sch := NewOpenSchedule(cfg.OpenSchedule)
	nonCfgSch := false
	{
		s, err := g.Settings.Find(scheduleKey)
		if err != nil {
			Logger.Errorf("read setting %q error: %v", scheduleKey, err)
		} else if s.ValueString() != "" {
			var m map[string]int
			err := json.Unmarshal([]byte(s.ValueString()), &m)
			if err != nil {
				Logger.Errorf("unmarshalling %q %q error: %v", scheduleKey, s.ValueString(), err)
			} else {
				sch = NewOpenSchedule(m)
				nonCfgSch = true
			}
		}
	}
	minuteTicker := time.NewTicker(time.Minute)
	var tenSecAfterErrorChan <-chan time.Time
	reset := false
	lastOpenedTimeNano := g.lastOpenedTime.Load()
	var lastGateOpenCommand *GateCommandAndText
	var lockedUntil time.Time
	var openMonitor OpenMonitor

Loop:
	for {
		select {
		case cmd := <-g.GateCommands:
			now := time.Now()
			cmd.text += now.In(Location).Format(" 0102 15:04:05")
			switch cmd.command {
			case Open:
				tenSecAfterErrorChan = nil
				if inOpenedState {
					if cmd.systemNotification == "" {
						Logger.Infof("ignoring open command in opened state for: %q", cmd.text)
					}
					continue
				}
				if now.Before(lockedUntil) {
					g.sendSystemNotification(fmt.Sprintf("IGNORED open command %q %q", cmd.systemNotification, cmd.text))
					continue
				}
				lastGateOpenCommand = cmd
				err := g.sendCommandToGate(cmd.text, now, cmd.command)
				g.updateLastOpenedTime(now)
				openMonitor.opened(now)
				if err != nil {
					tenSecAfterErrorChan = time.NewTicker(10 * time.Second).C
				}
				if cmd.systemNotification != "" {
					if err != nil {
						g.sendSystemNotification(fmt.Sprintf("%s error: %v", cmd.systemNotification, err))
					} else {
						g.sendSystemNotification(cmd.systemNotification)
					}
				}

			case KeepOpenBegin:
				tenSecAfterErrorChan = nil
				if inOpenedState {
					Logger.Infof("ignoring keep open command in opened state for: %q", cmd.text)
					continue
				}
				inOpenedState = true
				s := gate.Setting{Key: gateInOpenStateKey}
				s.SetBool(true)
				err := g.Settings.Update(&s)
				if err != nil {
					Logger.Errorf("error saving to db %q: %v", s.Key, err)
				}
				g.sendCommandToGate(cmd.text, now, cmd.command)

			case KeepOpenEnd:
				if !inOpenedState {
					Logger.Infof("ignoring close command in normal state for: %q", cmd.text)
					continue
				}
				inOpenedState = false
				s := gate.Setting{Key: gateInOpenStateKey}
				s.SetBool(false)
				err := g.Settings.Update(&s)
				if err != nil {
					Logger.Errorf("error saving to db %q: %v", s.Key, err)
				}
				g.sendCommandToGate(cmd.text, now, cmd.command)

			case Lock:
				minutes := cmd.args.(time.Duration)
				if minutes == 0 {
					lockedUntil = time.Time{}
				} else {
					lockedUntil = time.Now().Add(minutes)
				}

			case OpenedEvent:
				openMonitor.opened(now)

			}

		case <-tenSecAfterErrorChan:
			gateText, err := g.getGateTextValue(lastGateOpenCommand.command)
			if err != nil {
				break
			}
			if gateText == lastGateOpenCommand.text {
				tenSecAfterErrorChan = nil
				break
			}
			now := time.Now()
			err = g.sendCommandToGate(lastGateOpenCommand.text, now, lastGateOpenCommand.command)
			if err != nil {
				break
			}
			tenSecAfterErrorChan = nil
			g.updateLastOpenedTime(now)
			openMonitor.opened(now)

		case <-minuteTicker.C:
			if reset {
				minuteTicker.Reset(time.Minute)
				reset = false
			}
			now := time.Now()
			var lot time.Time
			if inOpenedState {
				lot = now
			} else {
				lot = time.Unix(0, g.lastOpenedTime.Load())
			}
			if lot.UnixNano() != lastOpenedTimeNano { // lot changed from prev tick
				lastOpenedTimeNano = lot.UnixNano()
				s := gate.Setting{Key: lastOpenedTimeKey}
				s.SetString(strconv.Itoa(int(lastOpenedTimeNano)))
				g.Settings.Update(&s)
			}
			if !inOpenedState {
				if openMonitor.isDoublingBefore(now.Add(-time.Minute)) {
					g.openGate("freeze-prevention", "")
					g.sendSystemNotification(fmt.Sprintf("OPENED by freeze-prevention %s", time.Now().In(Location).Format("15:04:05")))
				} else {
					minutes := sch.period(time.Now())
					remaining := time.Duration(minutes)*time.Minute - now.Sub(lot)
					if remaining < time.Minute {
						if remaining >= 5*time.Second {
							minuteTicker.Reset(remaining)
							reset = true
						} else {
							// assert: remaining < 5*time.Second
							g.openGate("timer", "")
							g.sendSystemNotification(fmt.Sprintf("OPENED by timer %s", time.Now().In(Location).Format("15:04:05")))
						}
					}
				}
			}
			g.syncGateRelayState(inOpenedState)

		case cfg = <-cfgSub:
			if !nonCfgSch {
				sch = NewOpenSchedule(cfg.OpenSchedule)
			}

		case m := <-g.schedule:
			s := gate.Setting{Key: scheduleKey}
			if len(m) != 0 {
				bytes, _ := json.Marshal(m)
				s.SetString(string(bytes))
			}
			err := g.Settings.Update(&s)
			if err != nil {
				Logger.Errorf("error saving to db %q: %v", s.Key, err)
			}
			nonCfgSch = len(m) != 0
			if len(m) == 0 {
				m = cfg.OpenSchedule
			}
			sch = NewOpenSchedule(m)

		case <-abort:
			break Loop
		}
	}
}

func (g *Gate) handlingScheduledJobs(abort <-chan struct{}, cfg *config.Config) {
	g.findAndApplyScheduledJobs()
	minuteTicker := time.NewTicker(time.Minute)
Loop:
	for {
		select {
		case <-minuteTicker.C:
			g.findAndApplyScheduledJobs()
		case <-abort:
			break Loop
		}
	}
}

func (g *Gate) findAndApplyScheduledJobs() error {
	ss, err := g.Settings.FindN(gate.ScheduledSettingsKeyPrefix)
	if err != nil {
		return err
	}
	for i := range *ss {
		s := &(*ss)[i]
		sch, err := s.Schedule()
		if err != nil {
			Logger.Errorf("parse %s %s setting error: %v", s.Key, s.ValueString(), err)
			continue
		}
		if !sch.IsValid() {
			Logger.Warnf("invalid setting %s %s", s.Key, s.ValueString())
			continue
		}
		now := time.Now()
		if !sch.IsTime(now) {
			continue
		}
		cmd := "/" + s.Key[len(gate.ScheduledSettingsKeyPrefix):]
		res, err := g.doHandleMattermostSysCommand(cmd, sch.Args)
		sch.ExecTimeMilli = now.UnixMilli()
		sch.ExecError = ""
		if err != nil {
			sch.ExecError = fmt.Sprintf("%v", err)
		}
		msg, ok := res.(string)
		if !ok && res != nil {
			var sb strings.Builder
			encoder := json.NewEncoder(&sb)
			encoder.Encode(res)
			msg = sb.String()
		}
		sch.ExecResult = msg
		yamlStr := gate.MarshalYAMLOneLine(&sch)
		s.SetString(yamlStr)
		if err != nil {
			Logger.Error(yamlStr)
		} else {
			Logger.Info(yamlStr)
		}
		g.Settings.Update(s)
	}
	return nil
}

func (g *Gate) syncGateRelayState(inOpenedState bool) error {
	value, err := g.getGateSwitchValue()
	if err != nil {
		return err
	}
	if value == inOpenedState {
		return nil
	}
	Logger.Warnf("relay state out of sync  %q", g.Cfg.GateRelaySwitchGetURL(g.Cfg.Gate.Relay.SwitchName))
	now := time.Now()
	if inOpenedState {
		g.sendCommandToGate(fmt.Sprintf("%s resynced", now.In(Location).Format("2006-01-02 15:04:05")), now, KeepOpenBegin)
	} else {
		g.sendCommandToGate(fmt.Sprintf("%s resynced", now.In(Location).Format("2006-01-02 15:04:05")), now, KeepOpenEnd)
	}
	return nil
}

func (g *Gate) getGateSwitchValue() (bool, error) {
	getURL := g.Cfg.GateRelaySwitchGetURL(g.Cfg.Gate.Relay.SwitchName)
	var result ESPHomeRelayResp
	err := g.doGateGet(getURL, &result)
	if err != nil {
		return false, err
	}
	return result.Value, nil
}

func (g *Gate) doGateGet(getURL string, result any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", getURL, nil)
	if err != nil {
		Logger.Errorf("%v", err)
		return err
	}
	req.SetBasicAuth(g.Cfg.Gate.User, g.Cfg.Gate.Pwd)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		Logger.Errorf("GET %q error: %v", getURL, err)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		Logger.Errorf("GET %q response: %d", getURL, resp.StatusCode)
		return fmt.Errorf("http %d", resp.StatusCode)
	}
	err = json.NewDecoder(resp.Body).Decode(result)
	if err != nil {
		Logger.Errorf("error unmarshalling relay state %q: %v", getURL, err)
		return err
	}
	return nil
}

func (g *Gate) sendCommandToGate(text string, now time.Time, cmd GateCommand) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	postURL := g.Cfg.GateRelayTextPostURL(g.gateESP32TextSensorName(cmd), url.QueryEscape(text))
	req, err := http.NewRequestWithContext(ctx, "POST", postURL, strings.NewReader(""))
	if err != nil {
		Logger.Errorf("%q: %v", postURL, err)
		return err
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(g.Cfg.Gate.User, g.Cfg.Gate.Pwd)

	g.lastOpenCommandTime.Store(now.Unix())

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		Logger.Errorf("error calling gate %q: %v", postURL, err)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		Logger.Errorf("error calling gate %q: %d", postURL, resp.StatusCode)
		return fmt.Errorf("http %d", resp.StatusCode)
	}
	Logger.Debugf("%q http %d", text, resp.StatusCode)
	return err
}

func (g *Gate) getGateTextValue(cmd GateCommand) (string, error) {
	getURL := g.Cfg.GateRelayTextGetURL(g.gateESP32TextSensorName(cmd))
	var result ESPHomeTextResp
	err := g.doGateGet(getURL, &result)
	if err != nil {
		return "", err
	}
	return result.Value, nil
}

func (g *Gate) gateESP32TextSensorName(cmd GateCommand) string {
	textName := ""
	switch cmd {
	case Open:
		textName = g.Cfg.Gate.Relay.OnOffTextName
	case KeepOpenBegin:
		textName = g.Cfg.Gate.Relay.OnTextName
	case KeepOpenEnd:
		textName = g.Cfg.Gate.Relay.OffTextName
	}
	return textName
}

type BLETrackingAggregator struct {
	g  *Gate
	tt []*BLETracking
}

func (a *BLETrackingAggregator) sendSystemNotification(p []*BLETracking) (empty bool) {
	for _, t := range p {
		a.tt = append(a.tt, t)
	}
	if len(a.tt) == 0 {
		return true
	}
	now := time.Now()
	var sb strings.Builder
	for i, t := range a.tt {
		if i != 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(t.StringNow(now))
		if s, ok := a.g.Cfg.BTMacNames[t.MAC]; ok {
			sb.WriteString(" ")
			sb.WriteString(s)
		}
	}
	a.tt = a.tt[:0]
	a.g.sendSystemNotification(sb.String())
	return false
}

type BLEGatekeeper struct {
	g              *Gate
	RecentlyOpened map[string]*BLETracking
}

func (k *BLEGatekeeper) init() {
	k.RecentlyOpened = make(map[string]*BLETracking)
}

func (k *BLEGatekeeper) checkAndOpen(p []*BLETracking, cfg *config.Config) {
	k.cleanRecentlyOpenedLongStanding(cfg)
	for _, bt := range p {
		if _, ok := k.RecentlyOpened[bt.MAC]; ok {
			continue
		}
		phone, ok := cfg.BTMacAutoOpenGate[bt.MAC]
		if !ok {
			continue
		}
		g := k.g
		u, ok := g.Phones[phone]
		if !ok {
			continue
		}
		if !g.allowedNow(phone) {
			g.sendSystemNotification(fmt.Sprintf("%s BLE restricted %s %s", bt.timestamp(), phone, u.name()))
			continue
		}
		if time.Since(time.Unix(0, g.lastOpenedTime.Load())) < 71*time.Second {
			continue
		}
		g.openGate(fmt.Sprintf("%s %s", bt.MAC, phone), "")
		g.sendSystemNotification(fmt.Sprintf("OPENED by BLE: %s (%s)  %s %s", bt.MAC, bt.timestamp(), phone, u.name()))
		break
	}
	for _, bt := range p {
		if t, ok := k.RecentlyOpened[bt.MAC]; ok && t.Time < bt.Time {
			k.RecentlyOpened[bt.MAC] = bt
		}
	}
}

func (k *BLEGatekeeper) cleanRecentlyOpenedLongStanding(cfg *config.Config) {
	BLEResumeAbsenceDuration := time.Duration(cfg.BLEResumeAbsenceDurationSec) * time.Second
	now := time.Now()
	for mac, bt := range k.RecentlyOpened {
		if now.Sub(bt.AsTime()) >= BLEResumeAbsenceDuration {
			delete(k.RecentlyOpened, mac)
		}
	}
}

type BLETrackingTimer struct {
	g         *Gate
	startTime time.Time
	lastTime  time.Time
}

func (a *BLETrackingTimer) openAfterPeriodOfActivity(p []*BLETracking, prolongedPresence time.Duration) {
	open := false
	for _, bt := range p {
		if _, ok := a.g.Cfg.BTMacAutoOpenGate[bt.MAC]; ok {
			continue
		}
		if s, ok := a.g.Cfg.BTMacNames[bt.MAC]; ok && strings.HasPrefix(s, "Ritsa:") {
			continue
		}
		tm := bt.AsTime()
		lag := tm.Sub(a.lastTime)
		if lag <= 0 {
			continue
		}
		if lag > time.Minute {
			a.startTime = tm
			a.lastTime = tm
			continue
		}
		a.lastTime = tm
		duration := a.lastTime.Sub(a.startTime)
		if duration < prolongedPresence {
			continue
		}
		reStartTime := time.Unix(0, a.g.lastOpenedTime.Load()).Add(time.Minute)
		if a.startTime.Sub(reStartTime) < 0 {
			a.startTime = reStartTime
			if a.lastTime.Sub(a.startTime) < 0 {
				a.lastTime = a.startTime
			}
			duration := a.lastTime.Sub(a.startTime)
			if duration < prolongedPresence {
				continue
			}
		}
		open = true
	}
	if open {
		a.g.openGate("BLE timer", "OPENED by BLE timer")
	}
}

type bleCount struct {
	cnt        int
	logCnt     int
	firstTime  time.Time
	lastTime   time.Time
	companyId  int
	companyCnt int
}

func (c *bleCount) age() time.Duration {
	return c.lastTime.Sub(c.firstTime)
}

func (g *Gate) handlingBLETracking(abort chan struct{}, cfg *config.Config, cfgSub chan *config.Config) {
	var firstWaitIsOver <-chan time.Time
	var nextWaitIsOver <-chan time.Time
	const firstDuration = 3 * time.Second
	const nextDuration = 30 * time.Second
	ticker := time.NewTicker(firstDuration)
	aggr := BLETrackingAggregator{g: g}
	bleGatekeeper := BLEGatekeeper{g: g}
	bleGatekeeper.init()
	bleTimer := BLETrackingTimer{g: g}
	sch := NewOpenSchedule(nil)
	s, err := g.Settings.Find(bleScheduleKey)
	if err != nil {
		Logger.Errorf("read setting %q error: %v", bleScheduleKey, err)
	} else if s.ValueString() != "" {
		var m map[string]int
		err := json.Unmarshal([]byte(s.ValueString()), &m)
		if err != nil {
			Logger.Errorf("unmarshalling %q %q error: %v", bleScheduleKey, s.ValueString(), err)
		} else {
			sch = NewOpenSchedule(m)
		}
	}
	bleAggr := make(map[string]*bleCount)
	wifiConnections := make(map[string]*NetworkClientInfo)
Loop:
	for {
		select {
		case btbt := <-g.bleTrackings:
			if len(btbt) == 0 {
				continue
			}
			loc := btbt[0].Location
			if loc == cfg.TestLocation {
				g.logTestBLETrackings(btbt, bleAggr)
			}
			if cfg.LogLocations[strconv.Itoa(loc)] {
				g.logBLETrackings(btbt)
			}
			// ignore if system location is unknown or not from system location
			if btbt[0].Location != 100 {
				continue
			}
			btbt = filterOut(btbt, cfg.BTMacIgnore)
			if len(btbt) == 0 {
				continue
			}
			bleGatekeeper.checkAndOpen(btbt, cfg)
			if firstWaitIsOver == nil && nextWaitIsOver == nil {
				ticker.Reset(firstDuration)
				firstWaitIsOver = ticker.C
			}
			aggr.sendSystemNotification(btbt)
			bleTimer.openAfterPeriodOfActivity(btbt, time.Duration(sch.period(time.Now()))*time.Minute)

		case v := <-g.wifiClients:
			switch ci := v.(type) {
			case *PALESLogInfo:
				now := time.Now()
				if now.Sub(ci.Time) > 5*time.Minute {
					break
				}
				var conn []*NetworkClientInfo
				for _, v := range wifiConnections {
					if now.Sub(v.Time) < 5*time.Minute {
						conn = append(conn, v)
					}
				}
				if len(conn) == 0 {
					break
				}
				var sb strings.Builder
				sb.WriteString("WiFi connections when opened by inet by ")
				sb.WriteString(ci.Phone)
				sb.WriteString(" ")
				sb.WriteString(ci.Firstname)
				sb.WriteString(" ")
				sb.WriteString(ci.Lastname)
				for _, c := range conn {
					sb.WriteString("\n")
					sb.WriteString(c.MAC)
					sb.WriteString(" ")
					sb.WriteString(c.Hostname)
					sb.WriteString(" ")
					sb.WriteString(c.IP)
					sb.WriteString(" connected ")
					sb.WriteString(now.Sub(c.Time).Round(time.Second).String())
					sb.WriteString(" ago")
					phone, name := phoneAndName(cfg, c)
					if phone != "" {
						sb.WriteString(" ")
						sb.WriteString(phone)
					}
					if name != "" {
						sb.WriteString(" ")
						sb.WriteString(name)
					}
				}
				g.sendSystemNotification(sb.String())

			case *NetworkClientInfo:
				if !ci.connected {
					delete(wifiConnections, ci.MAC)
					break
				}
				if prev, ok := wifiConnections[ci.MAC]; ok && ci.IP != prev.IP && prev.IP != "" {
					for k, v := range wifiConnections {
						if v.IP == ci.IP {
							delete(wifiConnections, k)
						}
					}
				}
				wifiConnections[ci.MAC] = ci
				phone, name := phoneAndName(cfg, ci)
				g.sendSystemNotification(fmt.Sprintf("WiFi: %s %s %s (%s) %s", ci.MAC, ci.IP, ci.Hostname, ci.Time, name))
				if phone == "" {
					continue
				}
				u, ok := g.Phones[phone]
				if !ok {
					continue
				}
				if !g.allowedNow(phone) {
					g.sendSystemNotification(fmt.Sprintf("WiFi restricted %s", u.name()))
					continue
				}
				g.openGate(fmt.Sprintf("WiFi %s %s", ci.MAC, phone), "")
				g.sendSystemNotification(fmt.Sprintf("OPENED by WiFi: %s (%s)  %s %s", ci.MAC, ci.Time, phone, u.name()))
			}

		case m := <-g.bleSchedule:
			sch = NewOpenSchedule(m)
			s := gate.Setting{Key: bleScheduleKey}
			if len(m) != 0 {
				bytes, _ := json.Marshal(m)
				s.SetString(string(bytes))
			}
			err := g.Settings.Update(&s)
			if err != nil {
				Logger.Errorf("error saving to db %q: %v", s.Key, err)
			}

		case <-firstWaitIsOver:
			ticker.Reset(nextDuration)
			firstWaitIsOver = nil
			nextWaitIsOver = ticker.C
			aggr.sendSystemNotification(nil)

		case <-nextWaitIsOver:
			if aggr.sendSystemNotification(nil) {
				nextWaitIsOver = nil
			}

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

		case cfg = <-cfgSub:

		case <-abort:
			break Loop
		}
	}
}

func phoneAndName(cfg *config.Config, nci *NetworkClientInfo) (string, string) {
	name, ok := cfg.WiFiMacNames[nci.MAC]
	if !ok && nci.Hostname != "" {
		name, _ = cfg.WiFiMacNames[nci.Hostname]
	}
	phone, ok := cfg.WiFiMACAutoOpenGate[nci.MAC]
	if !ok {
		if nci.Hostname != "" {
			phone, ok = cfg.WiFiMACAutoOpenGate[nci.Hostname]
		}
	}
	return phone, name
}

func (g *Gate) logTestBLETrackings(p []*BLETracking, bleAggr map[string]*bleCount) {
	now := time.Now()
	for _, bt := range p {
		c := bleAggr[bt.MAC]
		if c == nil {
			c = &bleCount{cnt: bt.Count, firstTime: now, lastTime: now, companyId: bt.CompanyId}
			bleAggr[bt.MAC] = c
			continue
		}
		c.cnt += bt.Count
		c.lastTime = now
		if bt.CompanyId != 0 && c.companyId != bt.CompanyId {
			c.companyCnt++
			c.companyId = bt.CompanyId
		}
		if c.age() < time.Duration(c.logCnt+1)*time.Minute {
			continue
		}
		c.logCnt++
		Logger.Debugf("%s CompanyId: %d NN: %d age: %s CompanyCnt: %d", bt.MAC, c.companyId, c.cnt,
			c.age().Round(time.Second).String(), c.companyCnt)
	}
	for k, v := range bleAggr {
		if now.Sub(v.lastTime) > 2*time.Minute {
			delete(bleAggr, k)
		}
	}
}

func (g *Gate) logBLETrackings(p []*BLETracking) {
	for _, bt := range p {
		bt.initRawData()
	}
	go func() {
		now := time.Now()
		for _, bt := range p {
			Logger.Debugf(bt.StringNow(now))
		}
	}()
}

func filterOut(p []*BLETracking, rem map[string]string) []*BLETracking {
	res := make([]*BLETracking, 0, len(p))
	for _, bt := range p {
		if _, ok := rem[bt.MAC]; ok {
			continue
		}
		res = append(res, bt)
	}
	return res
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
	codes := make(map[string]bool)
	cc, err := g.KeypadCodes.ListActive()
	if err != nil {
		Logger.Errorf("error reading db kpcodes: %v", err)
		return
	}
	for _, c := range cc {
		codes[c.Code] = true
	}
Loop:
	for {
		select {
		case sms := <-g.KeypadCodesRequests:
			now := time.Now()
			if sms.isTempCode() {
				badKeys := ""
				s, err := g.Settings.Find(badKeysKey)
				if err == nil {
					badKeys = s.ValueString()
				}
				length := 6
				if sms.Sms == "30m" {
					length = 5
				}
				code := generateCode(length, badKeys, codes)
				kpCode := &gate.KeypadCode{Code: code, RequesterPhone: sms.Phone, CreatedTimeMilli: time.Now().UnixMilli()}
				hours := sms.tempCodeTTLHours()
				smsText := ""
				if hours != 0 {
					kpCode.TTLMinutes = hours * 60
					smsText = fmt.Sprintf("код для шлагбаума %s. действителен %d ч с первого ввода", code, hours)
				} else {
					kpCode.EndTimeMilli = now.Add(30 * time.Minute).UnixMilli()
					smsText = fmt.Sprintf("код для шлагбаума %s. действителен 30 мин", code)
				}
				err = g.KeypadCodes.Insert(kpCode)
				if err != nil {
					Logger.Errorf("error saving to db kpcodes: %v", err)
					continue
				}
				g.sendSMS(sms.Phone, smsText, now.Add(20*time.Minute))
				continue
			}
		case <-abort:
			break Loop
		}
	}

}

func generateCode(length int, badKeys string, codes map[string]bool) string {
	return generateCodeFunc(rand.IntN, length, badKeys, codes)
}

func generateCodeFunc(randGen func(int) int, length int, badKeys string, codes map[string]bool) string {
	alphabet := ""
	base := 10 - len(badKeys)
	if base < 10 {
		for i := range 10 {
			d := strconv.Itoa(i)
			if strings.Contains(badKeys, d) {
				continue
			}
			alphabet += d
		}
	}
	min := int(math.Round(math.Pow(float64(base), float64(length-1))))
	max := int(math.Round(math.Pow(float64(base), float64(length)))) - 1
	code := ""
	for {
		n := randGen(max-min+1) + min
		if base == 10 {
			code = strconv.Itoa(n)
		} else {
			code = IntToBaseCustom(int64(n), alphabet)
		}
		if _, ok := codes[code]; !ok {
			break
		}
	}
	return code
}

func IntToBaseCustom(n int64, alphabet string) string {
	base := int64(len(alphabet))
	if base < 2 {
		return ""
	}
	if n == 0 {
		return string(alphabet[0])
	}
	result := ""
	for n > 0 {
		remainder := n % base
		result = string(alphabet[remainder]) + result
		n = n / base
	}
	return result
}

func (g *Gate) palesLoginAndLoadLoop(abort chan struct{}, topicEvents <-chan string, cfg *config.Config, cfgSub chan *config.Config) {
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
	g.loadPalESLogs(20 * time.Second)

	minuteLoginTicker := time.NewTicker(time.Minute)
	logsTikerMinutes := cfg.LogsTikerMinutes
	logsTicker := time.NewTicker(time.Duration(logsTikerMinutes) * time.Minute)
	thirtyMinuteTicker := time.NewTicker(30 * time.Minute)
	dailyTicker := time.NewTicker(24 * time.Hour)
Loop:
	for {
		select {
		case <-minuteLoginTicker.C:
			g.login(false)
		case <-logsTicker.C:
			g.loadPalESLogsOrLogin()
		case topic := <-topicEvents:
			if topic == "MQTT_ERROR" {
				continue
			}
			if strings.HasSuffix(topic, "/log") {
				g.loadPalESLogsOrLogin()
			}
		case <-thirtyMinuteTicker.C:
			g.loadPalesUsers()
		case <-dailyTicker.C:
			g.loadPalesTimeGroups()
		case cfg = <-cfgSub:
			if logsTikerMinutes == cfg.LogsTikerMinutes {
				continue
			}
			logsTikerMinutes = cfg.LogsTikerMinutes
			logsTicker.Reset(time.Duration(logsTikerMinutes) * time.Minute)

		case <-abort:
			break Loop
		}
	}
}

func (g *Gate) loadPalESLogsOrLogin() {
	st := g.loadPalESLogs(20 * time.Second)
	if st == http.StatusUnauthorized {
		g.login(true)
		g.loadPalESLogs(20 * time.Second)
	}
}

func (g *Gate) login(force bool) {
	if force {
		g.PalESPortalUserToken.Store("")
	} else if len(g.PalESPortalUserToken.Load().(string)) != 0 {
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
	g.PalESPortalUserToken.Store(result.User.Token)

	err = os.WriteFile(g.PalesTokenFilename, []byte(result.User.Token), 0644)
	if err != nil {
		Logger.Errorf("error writing file %s %v", g.PalesTokenFilename, err)
	}
}

func (g *Gate) loadPalESLogs(timeout time.Duration) int {
	token := g.PalESPortalUserToken.Load().(string)
	if len(token) == 0 {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	url := "https://portal.pal-es.com/api1/device/4G600215575/log?skip=0&limit=10&filter=&startDate=%s&endDate=&approved=&reasons=&rly=&type="
	//url = fmt.Sprintf(url, strconv.Itoa(int(g.palesLastLog.Tm)))
	url = fmt.Sprintf(url, "")
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		Logger.Errorf("%v", err)
		return -1
	}
	req.Header.Add("x-access-token", token)

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
	var result PalESLog
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
	slices.SortFunc(result.Log.List, func(a, b *PalesLogUser) int {
		return cmp.Compare(a.Tm, b.Tm)
	})
	n := 0
	for _, l := range result.Log.List {
		if g.palesLastLog.Tm > l.Tm {
			continue
		}
		if g.palesLastLog.UserId == l.UserId && g.palesLastLog.Tm == l.Tm && g.palesLastLog.Type == l.Type {
			continue
		}
		n++
		var opened string
		var approved string
		if l.Approved {
			t := l.Time()
			g.updateLastOpenedTime(t)
			g.GateCommands <- &GateCommandAndText{command: OpenedEvent, args: t}
			opened = "OPENED "
		} else {
			approved = fmt.Sprintf("!OK %d", l.Reason)
		}
		sn := ""
		if !strings.HasSuffix(l.UserId, l.Sn) {
			sn = l.Sn
		}
		phone := l.Phone()
		// {"UserId":"","Sn":"","Approved":false,"Type":100,"Tm":1779650919,"Reason":3,"Firstname":"Михаил","Lastname":""}
		if phone == "" {
			phone = g.findPhoneByName(l.Firstname, l.Lastname)
		}
		msg.WriteString(fmt.Sprintf("%s %s %s %s %s %s %s%s %s \n", opened, l.timestamp(), l.typeName(), phone, l.UserId, sn,
			l.Firstname, l.Lastname, approved))

		bb, err := json.Marshal(l)
		if err != nil {
			Logger.Debugf("%v", err)
		} else {
			Logger.Debug(string(bb))
		}
		if l.isInet() {
			g.wifiClients <- &PALESLogInfo{Phone: phone, Firstname: l.Firstname, Lastname: l.Lastname, Time: l.Time()}
		}
	}
	if n == 0 {
		return resp.StatusCode
	}
	g.palesLastLog = result.Log.List[len(result.Log.List)-1]
	Logger.Infof("received %d pal-es log records", len(result.Log.List))
	g.sendSystemNotification(msg.String())
	l := g.palesLastLog
	if l.Approved || l.isRemote() || !l.isWeakReason() ||
		time.Since(l.Time()) > 3*time.Minute ||
		time.Since(time.Unix(0, g.lastOpenedTime.Load())) < time.Minute {

		return resp.StatusCode
	}
	phone := l.Phone()
	// {"UserId":"","Sn":"","Approved":false,"Type":100,"Tm":1779650919,"Reason":3,"Firstname":"Михаил","Lastname":""}
	if phone == "" {
		phone = g.findPhoneByName(l.Firstname, l.Lastname)
	}
	if g.allowedNow(phone) {
		g.openGate(phone, "")
		g.sendSystemNotification(fmt.Sprintf("OPENED by received log %s  %s", phone, g.userName(phone, "")))
	}
	return resp.StatusCode
}

func (g *Gate) findPhoneByName(firstname, lastname string) string {
	phone := ""
	for p, u := range g.Phones {
		if u.Firstname == firstname && u.Lastname == lastname {
			if phone != "" {
				return ""
			}
			phone = p
		}
	}
	return phone
}

func (g *Gate) loadPalesTimeGroups() int {
	if g.palEsTimeGroups == nil {
		var tg PalEsTimeGroups
		tg.init()
		g.palEsTimeGroups = &tg
	}
	token := g.PalESPortalUserToken.Load().(string)
	if len(token) == 0 {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", "https://portal.pal-es.com/api1/device/4G600215575/groups", nil)

	if err != nil {
		Logger.Errorf("%v", err)
		return -1
	}
	req.Header.Add("x-access-token", token)

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
	token := g.PalESPortalUserToken.Load().(string)
	if len(token) == 0 {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://portal.pal-es.com/api1/device/4G600215575/users?skip=0&limit=10000&filter=", nil)

	if err != nil {
		Logger.Errorf("%v", err)
		return -1
	}
	req.Header.Add("x-access-token", token)

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
	Logger.Infof("entered keypad code %s  %s", c.Code, c.timestampSent())
	t := time.Unix(c.Time, 0)
	if !g.RateWatcher.hit(t) {
		g.sendSystemNotification(fmt.Sprintf("keypad code %s TOO MANY REQUESTS %s ", c.Code, c.timestampSent()))
		g.sendUserNotification(fmt.Sprintf("слишком много попыток ввода кода. подождите несколько минут %s ", c.timestampSent()))
		return Err429TooManyRequests
	}
	n := len(c.Code)
	badKeys := ""
	s, err := g.Settings.Find(badKeysKey)
	if err == nil {
		badKeys = s.ValueString()
	}
	switch {
	case true:
		if n <= 5 && strings.HasPrefix(c.Code, "0") {
			if c.Code == "000" {
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
			if badKeys != "" {
				break
			}
			g.sendUserNotification(fmt.Sprintf("неизвестный код %s", c.Code))
			return Err400BadFormat
		}
		if n == 5 || n == 6 {
			code, err := gate.Find(g.KeypadCodes, c.Code)
			if err != nil {
				Logger.Errorf("error finding kpcode %v", err)
				return Err400BadFormat
			}
			if code == nil {
				if badKeys != "" {
					break
				}
				g.sendUserNotification(fmt.Sprintf("код %s не найден или уже закончил свое действие", c.Code))
				return Err400BadFormat
			}
			g.openGateByCode(code)
			return nil
		}
		if n >= 9 && n <= 11 {
			if phone, ok := g.Cfg.MaskedPhones[c.Code]; ok {
				if u, ok := g.Phones[phone]; ok {
					if g.allowedNow(phone) {
						g.openGate(fmt.Sprintf("keypad %s", c.Code), "")
						g.sendSystemNotification(fmt.Sprintf("OPENED by keypad code %s %s %s", c.Code, u.name(), time.Now().In(Location).Format("15:04:05")))
						return nil
					} else {
						Logger.Warnf("keypad code !OK %s . masked phone is not allowed by register", phone)
					}
				} else {
					Logger.Warnf("keypad code !OK %s . masked phone is not found in gate register", phone)
				}
			}
		}
		// last 3 digits of phone and 6-digits totp code
		if n == 9 {
			totpCode := c.Code[n-6:]
			phonePostfix := c.Code[:n-6]
			phone := g.findTOTPPhoneByCode(phonePostfix, totpCode)
			if phone == "" {
				if badKeys != "" {
					break
				}
				return Err403Forbidden
			}
			u, ok := g.Phones[phone]
			if !ok {
				g.sendSystemNotification(fmt.Sprintf("keypad code %s is valid totp code for %s, but phone is not found in gate register", c.Code, phone))
				return Err403Forbidden
			}
			if !g.allowedNow(phone) {
				g.sendSystemNotification(fmt.Sprintf("keypad code: user restricted %s %s", c.Code, u.name()))
				return Err403Forbidden
			}
			g.openGate(fmt.Sprintf("keypad %s", c.Code), "")
			g.sendSystemNotification(fmt.Sprintf("OPENED by keypad code %s %s %s", c.Code, u.name(), time.Now().In(Location).Format("15:04:05")))
			return nil
		}
		// phone 79990010203 или 89990010203 или 9990010203
		if (n == 11 || n == 13 || n == 16) && (strings.HasPrefix(c.Code, "7") || strings.HasPrefix(c.Code, "8")) ||
			n == 10 && strings.HasPrefix(c.Code, "9") {

			phone := ""
			if n == 10 {
				phone = "7" + c.Code
			} else {
				phone = "7" + c.Code[1:11]
			}
			return g.phoneAsCodeEntered(phone, c, true)
		}
		if n == 12 && (strings.HasPrefix(c.Code, "79") || strings.HasPrefix(c.Code, "89")) {
			for i := 2; i < n; i++ {
				phone := "7" + c.Code[1:i] + c.Code[i+1:n]
				if _, ok := g.Phones[phone]; ok {
					return g.phoneAsCodeEntered(phone, c, true)
				}
			}
		}
		if n == 10 && (strings.HasPrefix(c.Code, "79") || strings.HasPrefix(c.Code, "89")) {
			phone := findPhoneWithMissingDigit(g.Phones, c.Code)
			if phone != "" {
				return g.phoneAsCodeEntered(phone, c, true)
			}
		}
	}
	if badKeys != "" {
		if n >= 3 && n < 6 {
			cc, err := g.KeypadCodes.ListActive()
			if err != nil {
				Logger.Errorf("error reading db kpcodes: %v", err)
			} else {
				for _, kc := range cc {
					if equalsLossy(badKeys, c.Code, kc.Code) {
						g.openGateByCode(&kc)
						return nil
					}
				}

			}
		}
		if n >= 5 && n < 11 {
			code := c.Code
			if strings.HasPrefix(code, "89") {
				code = code[1:]
			}
			if strings.HasPrefix(code, "9") {
				code = "7" + code
			}
			if !strings.HasPrefix(code, "79") {
				if strings.HasPrefix(code, "7") || strings.HasPrefix(code, "8") {
					code = "79" + code[1:]
				}
			}
			if strings.HasPrefix(code, "79") && len(code) >= 7 {
				phone := findPhoneWithFailingKeys(g.Phones, badKeys, code)
				if phone != "" {
					return g.phoneAsCodeEntered(phone, c, false)
				}
			}
		}
		// TODO на третий/n-ный раз (через сеттинг)
		s, err := g.Settings.Find(minCodeLenKey)
		if err == nil {
			minLen := s.ValueInt(0)
			if minLen != 0 && n >= minLen {
				g.openGate(fmt.Sprintf("keypad %s", c.Code), "")
				g.sendSystemNotification(fmt.Sprintf("OPENED by keypad code %s in FAKE mode %s", c.Code, time.Now().In(Location).Format("15:04:05")))
				return nil
			}
		}
	}
	g.sendSystemNotification(fmt.Sprintf("keypad code %s  %s", c.Code, c.timestampSent()))
	return Err400BadFormat
}

func (g *Gate) openGateByCode(code *gate.KeypadCode) {
	if code.EndTimeMilli == 0 {
		code.EndTimeMilli = time.Now().Add(time.Duration(code.TTLMinutes) * time.Minute).UnixMilli()
		g.KeypadCodes.Update(code)
	}
	g.openGate(fmt.Sprintf("keypad %s", code.Code), "")
	g.sendSystemNotification(fmt.Sprintf("OPENED by keypad code %s %s", code.Code, time.Now().In(Location).Format("15:04:05")))
	if code.Temporal() {
		g.sendUserNotification(fmt.Sprintf("гость %s успешно ввел код", maskPhone(code.RequesterPhone)))
	}
}

func equalsLossy(failingChars, lossyString, targetString string) bool {
	i, j := 0, 0
	for {
		if i == len(lossyString) {
			return true
		}
		if j == len(targetString) {
			return false
		}
		b := targetString[j]
		if lossyString[i] == b {
			i++
			j++
			continue
		}
		if strings.IndexByte(failingChars, b) < 0 {
			return false
		}
		j++
	}

}

func findPhoneWithMissingDigit(phones map[string]*PalESUser, p string) string {
	p = "7" + p[1:]
	res := ""
	for ph := range phones {
		if !equalsMissingDigit1(ph, p) {
			continue
		}
		if res != "" {
			return ""
		}
		res = ph
	}
	return res
}

func findPhoneWithFailingKeys(phones map[string]*PalESUser, failingKeys, p string) string {
	p = "7" + p[1:]
	res := ""
	for ph := range phones {
		if !equalsLossy(failingKeys, p, ph) {
			continue
		}
		if res != "" {
			return ""
		}
		res = ph
	}
	return res
}

func equalsMissingDigit1(s1, s2 string) bool {
	if s1[:7] == s2[:7] {
		return equalsMissingDigit2(s1[7:], s2[7:])
	}
	return s1[len(s1)-4:] == s2[len(s2)-4:] && equalsMissingDigit2(s1[2:7], s2[2:6])
}

func equalsMissingDigit2(s1, s2 string) bool {
	n := len(s2)
	for i := 0; i < n; i++ {
		if s1[i] == s2[i] {
			continue
		}
		return s1[i+1:] == s2[i:]
	}
	return true
}

func (g *Gate) phoneAsCodeEntered(phone string, c KeypadCode, smsIfNotFound bool) error {
	for _, v := range g.Cfg.MaskedPhones {
		if phone == v {
			g.sendSystemNotification(fmt.Sprintf("SOMEONE TRIED ENTER MASKED PHONE %s", phone))
			return nil
		}
	}
	u, ok := g.Phones[phone]
	if !ok {
		g.sendSystemNotification(fmt.Sprintf("keypad code !OK %s . phone is not found in gate register", phone))
		g.sendSMS(phone, "Ваш номер ввели на шлагбауме, не зарегистрирован в реестре. Обратитесь в правление.",
			time.Now().Add(24*time.Hour))
		return Err403Forbidden
	}
	if !g.allowedNow(phone) {
		g.sendSystemNotification(fmt.Sprintf("keypad code: user restricted %s %s", c.Code, u.name()))
		return Err403Forbidden
	}
	g.openGate(fmt.Sprintf("keypad %s", c.Code), "")
	g.sendSystemNotification(fmt.Sprintf("OPENED by keypad code %s %s %s", c.Code, u.name(), time.Now().In(Location).Format("15:04:05")))
	return nil
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

var ErrNotFound = errors.New("not found")

func (g *Gate) handleMattermostSysCommand(cmd, args string, encoder *json.Encoder) bool {
	res, err := g.doHandleMattermostSysCommand(cmd, args)
	if err == ErrNotFound {
		return false
	}
	msg, isStr := res.(string)
	if err != nil {
		errMsg := fmt.Sprintf("error: %v", err)
		if msg == "" {
			msg = errMsg
		} else {
			msg += " " + errMsg
		}
	}
	if !isStr {
		encoder.Encode(res)
	} else if msg != "" {
		encoder.Encode(NewMattermostResponse(msg))
	}
	return true
}

func (g *Gate) doHandleMattermostSysCommand(cmd, args string) (res any, err error) {
	switch cmd {
	case "/7_open":
		atts := &MattermostUIResponse{Attachments: []*MattermostUIAttachment{{Text: "Открыть шлагбаум?"}}}
		atts.Attachments[0].addAction(&MattermostUIAction{
			Id:    "open_yes",
			Name:  "Да",
			Type:  UITypeButton,
			Style: UIStylePrimary,
			Integration: MMUIIntegration{
				Url: fmt.Sprintf("%s/gate/mm/action", site),
				Context: MMUIContext{
					Action: UIActionOpen,
					Value:  true,
				},
			},
		})
		atts.Attachments[0].addAction(&MattermostUIAction{
			Id:    "open_no",
			Name:  "Нет",
			Type:  UITypeButton,
			Style: UIStyleDefault,
			Integration: MMUIIntegration{
				Url: fmt.Sprintf("%s/gate/mm/action", site),
				Context: MMUIContext{
					Action: UIActionOpen,
					Value:  false,
				},
			},
		})
		data, _ := json.Marshal(atts)
		Logger.Debug(string(data))
		return atts, nil

	case "/7_timer":
		text := strings.TrimSpace(args)
		var sch map[string]int
		if text == "" {
			g.schedule <- sch
			return "schedule is canceled", nil
		}
		var err error
		if strings.Contains(text, ":") {
			err = json.Unmarshal([]byte("{"+text+"}"), &sch)
		} else {
			sch = make(map[string]int)
			var n int
			n, err = strconv.Atoi(text)
			sch["00:00"] = n
		}
		if err != nil {
			return "", err
		}
		g.schedule <- sch
		bytes, _ := json.Marshal(sch)
		return fmt.Sprintf("%s schedule is set", strings.Trim(string(bytes), "{}")), nil

	case "/7_ble_timer":
		text := strings.TrimSpace(args)
		var sch map[string]int
		if text == "" {
			g.bleSchedule <- sch
			return "BLE schedule is canceled", nil
		}
		if strings.Contains(text, ":") {
			return "", json.Unmarshal([]byte("{"+text+"}"), &sch)
		}
		sch = make(map[string]int)
		n, err := strconv.Atoi(text)
		if err != nil {
			return "", err
		}
		sch["00:00"] = n
		g.bleSchedule <- sch
		bytes, _ := json.Marshal(sch)
		return fmt.Sprintf("%s BLE schedule is set", strings.Trim(string(bytes), "{}")), nil

	case "/7_open_after_m":
		text := strings.TrimSpace(args)
		if text == "" {
			return "n of minutes expected", nil
		}
		n, err := strconv.Atoi(text)
		if err != nil {
			return "", err
		}
		go func() {
			wait := time.Duration(n) * time.Minute
			time.Sleep(wait)
			lastTime := g.lastOpenedTime.Load()
			closedTime := time.Since(time.Unix(0, lastTime))
			if closedTime > wait {
				g.openGate(cmd, "")
				agoStr := "unknown"
				if lastTime != 0 {
					agoStr = (time.Duration(closedTime.Seconds()) * time.Second).String()
				}
				g.sendSystemNotification(fmt.Sprintf("opened by %s. previously opened %s ago", cmd, agoStr))
			}
		}()
		return fmt.Sprintf(
			"opening after %d minutes at %s", n,
			time.Now().Add(time.Duration(n)*time.Minute).In(Location).Format("15:04:05")), nil

	case "/7_keep_open":
		g.keepOpenGate()
		return "gate state changed to opened", nil

	case "/7_keep_open_cancel":
		g.endKeepOpenGate()
		return "gate state changed to normal", nil

	case "/7_lock":
		text := strings.TrimSpace(args)
		minutes := 0
		if text != "" {
			minutes, err = strconv.Atoi(text)
			if err != nil {
				return "number of minutes expected. 0 - unlock immediately", nil
			}
		}
		g.lock(time.Duration(minutes) * time.Minute)
		return "gate locked", nil

	case "/7_set":
		text := strings.TrimSpace(args)
		if text == "" || !strings.Contains(text, " ") {
			ss, err := g.Settings.FindN(text)
			if err != nil {
				return "", err
			}
			if len(*ss) == 0 {
				return "not found", nil
			}
			var msg strings.Builder
			for i, set := range *ss {
				if i != 0 {
					msg.WriteString("\n")
				}
				msg.WriteString(set.Key)
				msg.WriteString(" ")
				msg.WriteString(set.ValueString())
			}
			return msg.String(), nil
		}
		i := strings.Index(text, " ")
		key := text[:i]
		value := strings.TrimSpace(text[i+1:])
		n := len(value)
		value = strings.Trim(value, `"`)
		if len(value) == n {
			value = strings.Trim(value, "'")
		}
		set := gate.Setting{Key: key}
		if value == "" {
			err = g.Settings.Delete(&set)
			if err != nil {
				return "", err
			}
			return "deleted", nil
		}
		set.SetString(value)
		err := set.Validate()
		if err != nil {
			return "", err
		}
		err = g.Settings.Update(&set)
		if err != nil {
			return "", err
		}
		return "updated", nil

	default:
		return "", ErrNotFound
	}
}

func (g *Gate) handleMattermostUserCommand(req MattermostRequest, urlPath string, mmUser *gate.MattermostUser, encoder *json.Encoder) {
	switch req.Command {
	case "/7_totp_auth":
		// if !req.systemBotDirectMessage() {encoder.Encode(NewMattermostResponse("напишите это сообщение system-bot"))
		text := strings.TrimSpace(req.Text)
		if text == "" {
			if mmUser != nil {
				encoder.Encode(NewMattermostResponse(fmt.Sprintf(
					"Ваш подтвержденный номер телефона %s", maskPhone(mmUser.Phone))))
				return
			}
		}
		i := strings.LastIndex(text, " ")
		if i < 0 {
			encoder.Encode(NewMattermostResponse(fmt.Sprintf(
				"формат команды: %s <номер телефона> <код totp>  например: %s 79990010203 123456", req.Command, req.Command)))
			return
		}
		phone := text[:i]
		code := text[i+1:]
		replacer := strings.NewReplacer("+", "", "(", "", ")", "", "-", "", " ", "")
		phone = replacer.Replace(phone)
		if phone[:1] == "8" {
			phone = "7" + phone[1:]
		} else if phone[:1] != "7" {
			phone = "7" + phone
		}
		if len(phone) != 11 {
			encoder.Encode(NewMattermostResponse("номер телефона не распознан"))
			return
		}
		_, err := strconv.Atoi(phone)
		if err != nil {
			encoder.Encode(NewMattermostResponse("номер телефона не распознан"))
			return
		}
		if len(code) != 6 {
			encoder.Encode(NewMattermostResponse("неверный формат кода TOTP"))
			return
		}
		_, err = strconv.Atoi(code)
		if err != nil {
			encoder.Encode(NewMattermostResponse("неверный формат кода TOTP"))
			return
		}
		valid, err := validateTOTPCodeForPhone(phone, code)
		if err != nil {
			Logger.Errorf("%s TOTP validation error phone: %s code: %s  %v", urlPath, phone, code, err)
			encoder.Encode(NewMattermostResponse("неверный код TOTP"))
			return
		}
		if !valid {
			Logger.Infof("%s TOTP validation is not OK  phone: %s code: %s", urlPath, phone, code)
			encoder.Encode(NewMattermostResponse("неверный код TOTP"))
			return
		}
		u, err := g.MattermostUsers.Find(req.UserId)
		if err != nil {
			Logger.Errorf("db error: %v", err)
			encoder.Encode(NewMattermostResponse("внутренняя ошибка"))
			return
		}
		if u == nil {
			err = g.MattermostUsers.Insert(&gate.MattermostUser{UserId: req.UserId, Phone: phone})
			if err != nil {
				Logger.Errorf("db error: %v", err)
				encoder.Encode(NewMattermostResponse("внутренняя ошибка"))
				return
			}
			encoder.Encode(NewMattermostResponse("Ваш номер телефона подтвержден. Вам доступен функционал авторизованного пользоваеля."))
			return
		}
		if u.Phone == phone {
			encoder.Encode(NewMattermostResponse("Ваш номер телефона подтвержден (повторно). Вам доступен функционал авторизованного пользоваеля."))
			return
		}
		u.Phone = phone
		g.MattermostUsers.Update(u)
		encoder.Encode(NewMattermostResponse("Ваш номер телефона изменен."))

	}
}
