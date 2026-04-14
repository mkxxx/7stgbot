package tgsrv

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gocarina/gocsv"
	"github.com/robfig/cron/v3"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/pquerna/otp/totp"
	"github.com/skip2/go-qrcode"
	"github.com/xuri/excelize/v2"
)

const (
	paramNameSum          = "sum"
	paramNameYear         = "yyyy"
	paramNameMonth        = "mm"
	paramNamePrevElectr   = "prev"
	paramNameCurrElectr   = "curr"
	paramNameDebt         = "debt"
	paramNameFio          = "fio"
	paramNameNumber       = "n"
	paramNameHash         = "h"
	paramNamePrevKey      = "prevyyyymmnumber"
	paramNamePurpose      = "purpose"
	paramNamePrice        = "price"
	paramNameCoef         = "coef"
	qrImgPath             = "/images/qr.jpg"
	qreImgPath            = "/images/qre.jpg"
	qrcImgPath            = "/images/qrc.jpg"
	qrPath                = "/docs/qr/"
	payPath               = "/docs/оплата/"
	payElectrPath         = "/docs/оплата-эл/"
	contactsPath          = "/docs/contacts/"
	internetPath          = "/docs/internet/"
	internetElectrCSVPath = "/docs/electr.csv/"
	internetDocsPath      = "/docs/"
	blePath               = "/ble/"
	ble2Path              = "/ble2/"
	gateOnCallPath        = "/gate/call/"
	gateOpenedPath        = "/gate/opened/"
	gateOnSmsPath         = "/gate/sms/"
	gateKeypadPath        = "/gate/keypad/"
	logLevelPath          = "/app/log/"
	genQRCodePath         = "/gate/qr/"
	gateAutomateCallPath  = "/gate/automate/call/"
	gateAutomateSMSPath   = "/gate/automate/sms/"

	site = "https://7slavka.ru"

	QRSalt = "ee1e4a719ddac2689"

	// required
	QRHeader          = "ST00012"
	QRNameName        = "Name"        // <= 160
	QRNamePersonalAcc = "PersonalAcc" // <= 20
	QRNameBankName    = "BankName"    // <= 45
	QRNameBIC         = "BIC"         // <= 9
	QRNameCorrespAcc  = "CorrespAcc"  // <= 20
	// optional
	QRNamePurpose  = "Purpose"  // <= 210
	QRNameSum      = "Sum"      // <= 18
	QRNameLastName = "LastName" // <= 18
	QRNamePayeeINN = "PayeeINN" // <= 12
)

var digitsRE = regexp.MustCompile(`^[0-9]+$`)

// {"mac":"5B:00:DF:94:DD:1C","uuid":"","rssi":-71,"name":"iTAG  ","company_id":56604,"location":2,"time":1775136766}
type BLETracking struct {
	MAC       string
	RSSI      int
	Name      string
	UUID      string
	CompanyId int `json:"company_id"`
	Location  int
	Time      int64 // seconds
}

func (t *BLETracking) timestamp() string {
	return time.Unix(t.Time, 0).In(Location).Format("2006-01-02 15:04:05")
}

type PhoneCall struct {
	Phone        string
	UnixTime     float64 `json:"time"`
	CalledNumber string  `json:"called_number"`
}

func (c *PhoneCall) time() time.Time {
	seconds := int64(c.UnixTime)
	return time.Unix(seconds, int64((c.UnixTime-float64(seconds))*float64(time.Nanosecond)))
}

func (c *PhoneCall) timestamp() string {
	return c.time().In(Location).Format("2006-01-02 15:04:05")
}

type PhoneSms struct {
	Phone        string `json:"sender_phone_number"`
	Sms          string
	SentUnixTime string `json:"timestamp_sent"`
}

func (s *PhoneSms) timestampSent() string {
	unix, err := strconv.Atoi(s.SentUnixTime)
	if err != nil {
		return ""
	}
	return time.Unix(int64(unix), 0).In(Location).Format("2006-01-02 15:04:05")
}

func (s *PhoneSms) isTOTP() bool {
	return strings.ToLower(strings.TrimSpace(s.Sms)) == "totp"
}

func (s *PhoneSms) isTempCode() bool {
	text := strings.ToLower(strings.TrimSpace(s.Sms))
	return text == "30m" || text == ".48h." || text == ".16h."
}

func (s *PhoneSms) tempCodeTTLHours() int {
	text := strings.ToLower(strings.TrimSpace(s.Sms))
	switch text {
	case ".48h.":
		return 48
	case ".16h.":
		return 16
	}
	return 0
}

type OpenTime struct {
	Time int64
}

func (t *OpenTime) timestampSent() string {
	return time.Unix(t.Time, 0).In(Location).Format("2006-01-02 15:04:05")
}

type KeypadCode struct {
	Code string
	Time int64
}

func (t *KeypadCode) timestampSent() string {
	return time.Unix(t.Time, 0).In(Location).Format("2006-01-02 15:04:05")
}

func init() {
	rand.Seed(time.Now().Unix())
}

func StartWebServer(port int, staticDir, dir string, QRElements map[string]string, price map[string]float64,
	coef map[string]float64, abort chan struct{}, pinger *pingMonitor, g *Gate) *webSrv {

	webServer := newWebServer(port, staticDir, dir, QRElements, price, coef, pinger, abort, g)
	webServer.start(port)
	srv := webServer.httpServer
	go func() {
		<-abort
		srv.Shutdown(context.Background())
	}()
	return webServer
}

func newWebServer(port int, staticDir string, dir string, QRElements map[string]string,
	price map[string]float64, coef map[string]float64, pinger *pingMonitor, abort chan struct{},
	g *Gate) *webSrv {

	ws := new(webSrv)
	(&ws.priceHist).fromMap(price)
	ws.QRElements = QRElements
	ws.staticDir = staticDir
	ws.dataDir = dir
	ws.pinger = pinger
	(&ws.coefHist).fromMap(coef)
	ws.gate = g

	fs := http.FileServer(http.Dir(staticDir))
	//ws.staticHandler = http.StripPrefix("/static/", fs)
	ws.staticHandler = fs
	ws.abort = abort

	ws.loadSntClubUsers()

	ws.registry.Store(loadRegistry(dir))
	c := cron.New(cron.WithSeconds(), cron.WithLocation(Location))
	_, err := c.AddFunc("0 45 * * * *", func() {
		r := loadRegistry(dir)
		ws.setRegistry(r)
	})
	if err != nil {
		log.Println(err)
	}
	c.Start()
	go func() {
		<-abort
		c.Stop()
	}()

	go g.palesLoginAndLoadLoop(abort)
	go g.handlingCalls(abort)
	go g.handlingSmses(abort)
	go g.handlingBLETracking(abort)
	go g.readingSMSesForSend(abort)
	go g.handlingKeypadRequests(abort)
	go g.sendingSystemNotification(abort)
	go g.sendingUserNotification(abort)

	http.HandleFunc("/", ws.handle)

	ws.httpServer = &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: nil}
	return ws
}

func (ws *webSrv) setRegistry(r *Registry) {
	if len(r.searchResult.Records) != 0 && len(r.registry) != 0 {
		ws.registry.Store(r)
		return
	}
	if len(r.searchResult.Records) == 0 && len(r.registry) == 0 {
		return
	}
	registry := ws.registry.Load().(*Registry)
	if len(r.searchResult.Records) == 0 {
		r.searchResult = registry.searchResult
		r.searchRecords = registry.searchRecords
	}
	if len(r.registry) == 0 {
		r.registry = registry.registry
	}
	ws.registry.Store(r)
}

type valueDates []valueDate

type valueDate struct {
	date  string
	value float64
}

func (vd *valueDates) fromMap(p map[string]float64) {
	*vd = make([]valueDate, 0, len(p))
	dates := make([]string, 0, len(p))
	for k := range p {
		dates = append(dates, k)
	}
	sort.Strings(dates)
	for i := len(dates) - 1; i >= 0; i-- {
		d := dates[i]
		*vd = append(*vd, valueDate{date: d, value: p[d]})
	}
}

func (vd *valueDates) coef(year int, month int) float64 {
	if vd == nil {
		return 0
	}
	d := fmt.Sprintf("%d%02d", year, month)
	var dv valueDate
	for _, dv = range *vd {
		if d >= dv.date {
			return dv.value
		}
	}
	return dv.value
}

func (vd *valueDates) coefStr(year string, month string) float64 {
	y, err := strconv.Atoi(year)
	if err != nil {
		return 0
	}
	m, err := strconv.Atoi(month)
	if err != nil {
		return 0
	}
	return vd.coef(y, m)
}

type webSrv struct {
	priceHist     valueDates
	coefHist      valueDates
	QRElements    map[string]string
	staticDir     string
	dataDir       string
	staticHandler http.Handler
	httpServer    *http.Server
	pinger        *pingMonitor
	registry      atomic.Value
	gate          *Gate
	abort         chan struct{}
}

func (s *webSrv) start(port int) {
	go func() {
		log.Printf("Listening on :%d...", port)
		err := s.httpServer.ListenAndServe()
		Logger.Debug("Web server stopped")
		if err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
}

func (s *webSrv) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == payPath {
		if r.Method == "GET" {
			s.servePayTemplate(w, r)
			return
		}
		if r.Method == "POST" {
			// Call ParseForm() to parse the raw query and update r.PostForm and r.Form.
			if err := r.ParseForm(); err != nil {
				Logger.Errorf("ParseForm() err: %v", err)
				http.Error(w, fmt.Sprintf("500 error %v", err), http.StatusInternalServerError)
				return
			}
			params := url.Values{}
			params.Add(paramNameSum, r.FormValue(paramNameSum))
			params.Add(paramNamePurpose, r.FormValue(paramNamePurpose))
			params.Add(paramNameFio, r.FormValue(paramNameFio))
			http.Redirect(w, r, r.URL.Path+"?"+params.Encode(), http.StatusFound)
			Logger.Info("POST: ", join(params))
			return
		}
		return
	}
	if r.URL.Path == payElectrPath {
		if r.Method == "GET" {
			s.servePayElectrTemplate(w, r)
			return
		}
		if r.Method == "POST" {
			// Call ParseForm() to parse the raw query and update r.PostForm and r.Form.
			if err := r.ParseForm(); err != nil {
				Logger.Errorf("ParseForm() err: %v", err)
				http.Error(w, fmt.Sprintf("500 error %v", err), http.StatusInternalServerError)
				return
			}
			number := r.FormValue(paramNameNumber)
			prevKey := r.FormValue(paramNamePrevKey)
			prev := r.FormValue(paramNamePrevElectr)
			curr := r.FormValue(paramNameCurrElectr)
			debt := r.FormValue(paramNameDebt)
			if len(number) != 0 && len(prevKey) != 0 && number != prevKey {
				prev = ""
				curr = ""
				debt = ""
			}
			params := url.Values{}
			params.Add(paramNameYear, r.FormValue(paramNameYear))
			params.Add(paramNameMonth, r.FormValue(paramNameMonth))
			params.Add(paramNameNumber, number)
			params.Add(paramNamePrevElectr, prev)
			params.Add(paramNameCurrElectr, curr)
			params.Add(paramNameDebt, debt)
			params.Add(paramNameFio, r.FormValue(paramNameFio))
			params.Add(paramNamePrice, r.FormValue(paramNamePrice))
			params.Add(paramNameCoef, r.FormValue(paramNameCoef))
			http.Redirect(w, r, r.URL.Path+"?"+params.Encode(), http.StatusFound)
			Logger.Info("POST: ", join(params))
			return
		}
		return
	}
	if r.URL.Path == qrPath {
		if r.Method == "GET" {
			s.serveQRTemplate(w, r)
			return
		}
		return
	}
	if r.URL.Path == qrImgPath {
		query := r.URL.Query()
		s.writeImage(w,
			query.Get(paramNameSum),
			query.Get(paramNamePurpose),
			query.Get(paramNameFio),
		)
		return
	}
	if strings.HasPrefix(r.URL.Path, genQRCodePath) {
		phone := r.URL.Path[len(genQRCodePath):]
		strings.TrimSuffix(phone, "/")
		if len(phone) != 10 || !digitsRE.MatchString(phone) {
			http.Error(w, "10 digits expected", http.StatusBadRequest)
			return
		}
		s.generateTOTPQRCodeImage(w, phone)
		return
	}
	if r.URL.Path == qreImgPath {
		query := r.URL.Query()
		sum, purpose := s.calculate(
			query.Get(paramNameYear),
			query.Get(paramNameMonth),
			query.Get(paramNameNumber),
			query.Get(paramNamePrevElectr),
			query.Get(paramNameCurrElectr),
			query.Get(paramNameDebt),
			query.Get(paramNamePrice),
			query.Get(paramNameCoef),
			query.Get(paramNameFio),
		)
		s.writeImage(w,
			sum,
			purpose,
			query.Get(paramNameFio),
		)
		return
	}
	if r.URL.Path == qrcImgPath {
		query := r.URL.Query()
		year := query.Get(paramNameYear)
		month := query.Get(paramNameMonth)
		number := query.Get(paramNameNumber)
		hash := query.Get(paramNameHash)
		var sum float64
		var purpose template.HTML

		var ok bool
		if checkHash(year, month, number, hash) {
			purpose, sum, ok = s.purpose(year, month, number)
		}
		if ok {
			s.writeImage(w,
				fmt.Sprintf("%.2f", sum),
				string(purpose),
				"",
			)
		}
		return
	}
	if r.URL.Path == "/" || r.URL.Path == contactsPath {
		type tdataType struct {
			DivAlignRight template.HTML
			DivEnd        template.HTML
		}
		tdata := &tdataType{
			DivAlignRight: template.HTML(`<div style="text-align: right">`),
			DivEnd:        template.HTML(`</div>`),
		}
		s.serveTemplate(w, r, tdata, nil)
		return
	}
	if r.URL.Path == internetPath {
		type tdataType struct {
			PingResult     template.HTML
			OnlineRecently int
			Reached        int
		}
		tdata := &tdataType{
			PingResult: template.HTML(""),
		}
		tdata.OnlineRecently, tdata.Reached = s.pinger.onlineCount()
		query := r.URL.Query()
		if !query.Has("ping") {
			s.serveTemplate(w, r, tdata, nil)
			return
		}
		ip := s.pinger.bestIP(10)
		if len(ip) == 0 {
			s.serveTemplate(w, r, tdata, nil)
			return
		}
		var buf bytes.Buffer
		pingIp(&buf, ip)
		tdata.PingResult = template.HTML(buf.String())
		if query.Get("ping") == "2" {
			tdata.PingResult += "\n\n"
			ips := s.pinger.IPs(false)
			sort.Sort(byIP(ips))
			for _, ip := range ips {
				tdata.PingResult += template.HTML(ip + "\n")
			}
		}
		s.serveTemplate(w, r, tdata, nil)
		return
	}
	if r.URL.Path == internetElectrCSVPath {
		query := r.URL.Query()
		year := query.Get(paramNameYear)
		y, err := strconv.Atoi(year)
		if err != nil {
			Logger.Errorf("expected 4-digits year %s %v", year, err)
			return
		}
		month := query.Get(paramNameMonth)
		m, err := strconv.Atoi(month)
		if err != nil {
			Logger.Errorf("expected 2-digits month %s %v", month, err)
			return
		}
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", "attachment;filename=electr_"+
			year+month+".csv")
		items := LoadElectrForMonth(s.staticDir, y, m)
		items = anonymize(items)
		for _, it := range items {
			if len(it.PlotNumber) == 0 {
				continue
			}
			it.QRURL = QRURL(year, month, it.PlotNumber)
			registry := s.registry.Load().(*Registry)
			email := registry.getEmailByPlotNumber(it.PlotNumber)
			if len(email) != 0 {
				it.BotURL = BotURL(email)
			}
		}
		gocsv.SetCSVWriter(func(w io.Writer) *gocsv.SafeCSVWriter {
			csvWriter := gocsv.DefaultCSVWriter(w)
			csvWriter.Comma = ';'
			return csvWriter
		})
		gocsv.Marshal(items, w)
	}
	if r.URL.Path == internetDocsPath {
		s.serveTemplate(w, r, Bool(false), func(s string) string {
			return strings.Replace(s, "<script ", "{{end}}\n <script ", 1)
		})
		return
	}
	if r.URL.Path == blePath {
		if r.Method != "POST" {
			http.Error(w, "Resource not found", http.StatusNotFound)
			return
		}
		defer r.Body.Close()
		r.Body = http.MaxBytesReader(w, r.Body, 1048576)

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			Logger.Errorf("%s cannot read request body %v", r.URL.Path, err)
			http.Error(w, "cannot read request body", http.StatusInternalServerError)
			return
		}
		var bleTracking BLETracking
		if err := json.NewDecoder(bytes.NewReader(bodyBytes)).Decode(&bleTracking); err != nil {
			Logger.Errorf("%s cannot read request body %v  %s", r.URL.Path, err, string(bodyBytes))
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if Logger.Level().Enabled(zap.DebugLevel) {
			Logger.Debugf("Received BLE: MAC: %s, RSSI: %d, Name: %s, Location: %d   %s",
				bleTracking.MAC, bleTracking.RSSI, bleTracking.Name, bleTracking.Location, string(bodyBytes))
		} else {
			Logger.Infof("Received BLE: MAC: %s, RSSI: %d, Name: %s, Location: %d",
				bleTracking.MAC, bleTracking.RSSI, bleTracking.Name, bleTracking.Location)
		}
		s.gate.bleTrackings <- &bleTracking
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.URL.Path == ble2Path {
		if r.Method != "POST" {
			http.Error(w, "Resource not found", http.StatusNotFound)
			return
		}
		defer r.Body.Close()
		r.Body = http.MaxBytesReader(w, r.Body, 1048576)

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			Logger.Errorf("%s cannot read request body %v", r.URL.Path, err)
			http.Error(w, "cannot read request body", http.StatusInternalServerError)
			return
		}
		var bleTrackings []*BLETracking
		if err := json.NewDecoder(bytes.NewReader(bodyBytes)).Decode(&bleTrackings); err != nil {
			Logger.Errorf("%s cannot read request body %v  %s", r.URL.Path, err, string(bodyBytes))
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		for _, bt := range bleTrackings {
			Logger.Debugf("Received BLE: MAC: %s, RSSI: %d, Name: %s, Location: %d",
				bt.MAC, bt.RSSI, bt.Name, bt.Location)
			s.gate.bleTrackings <- bt
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.URL.Path == gateOnCallPath {
		if r.Method != "POST" {
			http.Error(w, "Resource not found", http.StatusNotFound)
			return
		}
		defer r.Body.Close()
		r.Body = http.MaxBytesReader(w, r.Body, 1048576)

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			Logger.Errorf("%s cannot read request body %v", r.URL.Path, err)
			http.Error(w, "cannot read request body", http.StatusInternalServerError)
			return
		}
		var phoneCall PhoneCall
		if err := json.NewDecoder(bytes.NewReader(bodyBytes)).Decode(&phoneCall); err != nil {
			Logger.Errorf("%s cannot read request body %v  %s", r.URL.Path, err, string(bodyBytes))
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		Logger.Infof("Call received: %s   %s", phoneCall.Phone, string(bodyBytes))
		s.gate.phoneCalls <- &phoneCall
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.URL.Path == gateOnSmsPath {
		if r.Method != "POST" {
			http.Error(w, "Resource not found", http.StatusNotFound)
			return
		}
		defer r.Body.Close()
		r.Body = http.MaxBytesReader(w, r.Body, 1048576)

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			Logger.Errorf("%s cannot read request body %v", r.URL.Path, err)
			http.Error(w, "cannot read request body", http.StatusInternalServerError)
			return
		}
		var phoneSms PhoneSms
		if err := json.NewDecoder(bytes.NewReader(bodyBytes)).Decode(&phoneSms); err != nil {
			Logger.Errorf("%s cannot read request body %v  %s", r.URL.Path, err, string(bodyBytes))
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		Logger.Infof("Sms received: %s   %s", phoneSms.Phone, string(bodyBytes))
		s.gate.phoneSmses <- &phoneSms
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.URL.Path == gateOpenedPath {
		if r.Method != "POST" {
			http.Error(w, "Resource not found", http.StatusNotFound)
			return
		}
		defer r.Body.Close()
		r.Body = http.MaxBytesReader(w, r.Body, 1048576)

		bodyBytes, err := io.ReadAll(r.Body)
		var openTime OpenTime
		if err != nil {
			Logger.Errorf("%s cannot read request body %v", r.URL.Path, err)
		} else if err := json.NewDecoder(bytes.NewReader(bodyBytes)).Decode(&openTime); err != nil {
			Logger.Errorf("%s cannot parse request body %v  %q", r.URL.Path, err, string(bodyBytes))
		} else {
			Logger.Debugf("%s  %q", r.URL.Path, string(bodyBytes))
		}
		w.WriteHeader(http.StatusOK)
		s.gate.openedEvets <- openTime
		return
	}
	if r.URL.Path == logLevelPath {
		if r.Method != "POST" {
			http.Error(w, "Resource not found", http.StatusNotFound)
			return
		}
		defer r.Body.Close()
		r.Body = http.MaxBytesReader(w, r.Body, 1048576)

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			Logger.Errorf("%s cannot read request body %v", r.URL.Path, err)
			http.Error(w, "cannot read request body", http.StatusInternalServerError)
			return
		}
		l, err := strconv.Atoi(string(bodyBytes))
		if err != nil {
			Logger.Errorf("%s cannot read request body %v  %s", r.URL.Path, err, string(bodyBytes))
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if l < -1 || l > 3 {
			Logger.Errorf("%s bad value %s", r.URL.Path, string(bodyBytes))
			http.Error(w, "bad value", http.StatusBadRequest)
			return
		}
		AtomicLevel.SetLevel(zapcore.Level(l))
		return
	}
	if r.URL.Path == gateKeypadPath {
		if r.Method != "POST" {
			http.Error(w, "Resource not found", http.StatusNotFound)
			return
		}
		// 400 - bad format, 403 - forbidden, 429 - too many requests
		defer r.Body.Close()
		r.Body = http.MaxBytesReader(w, r.Body, 1048576)

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			Logger.Errorf("%s cannot read request body %v", r.URL.Path, err)
			http.Error(w, "cannot read request body", http.StatusInternalServerError)
			return
		}
		var keypadCode KeypadCode
		if err := json.NewDecoder(bytes.NewReader(bodyBytes)).Decode(&keypadCode); err != nil {
			Logger.Errorf("%s cannot read request body %v  %s", r.URL.Path, err, string(bodyBytes))
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		Logger.Debugf("keypad %s", string(bodyBytes))

		err = s.gate.keypadCode(keypadCode)
		switch {
		case errors.Is(err, Err400BadFormat):
			http.Error(w, err.Error(), http.StatusBadRequest)
		case errors.Is(err, Err403Forbidden):
			http.Error(w, err.Error(), http.StatusForbidden)
		case errors.Is(err, Err429TooManyRequests):
			http.Error(w, err.Error(), http.StatusTooManyRequests)
		case err != nil:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusOK)
		}
		return
	}
	if r.URL.Path == gateAutomateCallPath {
		if r.Method != "POST" {
			http.Error(w, "Resource not found", http.StatusNotFound)
			return
		}
		for {
			select {
			case c := <-s.gate.PendingCalls:
				if c.Deadline < time.Now().UnixMilli() {
					continue
				}
				fmt.Fprintf(w, `{"phone": "%s"}`, c.Phone)
				return
			case <-s.abort:
				return
			}
		}
	}
	if r.URL.Path == gateAutomateSMSPath {
		if r.Method != "POST" {
			http.Error(w, "Resource not found", http.StatusNotFound)
			return
		}
		defer r.Body.Close()
		r.Body = http.MaxBytesReader(w, r.Body, 1048576)

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			Logger.Errorf("%s cannot read request body %v", r.URL.Path, err)
			http.Error(w, "cannot read request body", http.StatusInternalServerError)
			return
		}
		timer55s := time.NewTimer(time.Second * 55)
		var automateReq AutomateReq
		if err := json.NewDecoder(bytes.NewReader(bodyBytes)).Decode(&automateReq); err != nil {
			Logger.Errorf("%s cannot read request body %v  %s", r.URL.Path, err, string(bodyBytes))
			http.Error(w, err.Error(), http.StatusBadRequest)
			select {
			case <-timer55s.C:
			case <-s.abort:
			}
			return
		}
		for {
			select {
			case m := <-s.gate.PendingSMSes:
				if m.Expired() {
					Logger.Debugf("expired SMS: %s %q", m.Phone, m.Msg)
					continue
				}
				automateSMS := &AutomateSMS{Phone: m.Phone, Text: m.Msg}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				text := fmt.Sprintf("phone: %s, text: %q", m.Phone, m.Msg)
				err := json.NewEncoder(w).Encode(automateSMS)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					Logger.Errorf("%s error serializing response %v  %s", r.URL.Path, err, text)
				} else {
					Logger.Infof("%s <- %s", r.URL.Path, text)
				}
				m.Sent()
				s.gate.SMSes.Update(m)
				s.gate.sendSystemNotification(fmt.Sprintf("sent SMS: %s %q", m.Phone, m.Msg))
				return

			case <-timer55s.C:
				w.WriteHeader(http.StatusRequestTimeout)
				return

			case <-s.abort:
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
		}
	}
	Logger.Debugf("static resource %s", r.URL.Path)
	s.staticHandler.ServeHTTP(w, r)
}

func QRURL(year string, month string, plotNumber string) string {
	params := url.Values{}
	params.Add("yyyy", year)
	params.Add("mm", month)
	params.Add("n", plotNumber)
	params.Add("h", sha1Hash(year, month, plotNumber))
	x := site + qrPath + "?" + params.Encode()
	return x
}

func QRURLInt(year int, month int, plotNumber string) string {
	return QRURL(strconv.Itoa(year), fmt.Sprintf("%02d", month), plotNumber)
}

func BotURL(email string) string {
	return `https://t.me/snt7s_bot?start=` + encodeEmailAndMD5(email)
}

type Bool bool

func (b *Bool) OK() bool {
	return bool(*b)
}

func (b *Bool) NotOK() bool {
	return !b.OK()
}

func (b *Bool) True() bool {
	return true
}

func (b *Bool) False() bool {
	return false
}

func anonymize(p []*ElectrEvidence) []*ElectrEvidence {
	items := make([]*ElectrEvidence, 0, len(p))
	for _, ev := range p {
		c := ev.Copy()
		c.FIO = ""
		items = append(items, c)
	}
	return items
}

type byIP []string

func (a byIP) Len() int      { return len(a) }
func (a byIP) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a byIP) Less(i, j int) bool {
	x := "  " + a[i][strings.LastIndex(a[i], "."):]
	y := "  " + a[j][strings.LastIndex(a[j], "."):]
	return x[len(x)-3:] < y[len(y)-3:]
}

func pingIp(w io.Writer, ip string) {
	out, _ := exec.Command("ping", ip, "-c", "3", "-w", "5").CombinedOutput()
	fmt.Fprintf(w, "%s", string(out))
	/*	pinger, err := ping.NewPinger(ip)
		if err != nil {
			Logger.Errorf("could not create pinger 91.234.180.53")
			return
		}
		go func() {
			timer := time.NewTimer(time.Second * 5)
			<-timer.C
			pinger.Stop()
		}()
		pinger.OnRecv = func(pkt *ping.Packet) {
			_, _ = fmt.Fprintf(w, "%d bytes from %s: icmp_seq=%d time=%v\n",
				pkt.Nbytes, pkt.IPAddr, pkt.Seq, pkt.Rtt)
		}
		pinger.OnDuplicateRecv = func(pkt *ping.Packet) {
			_, _ = fmt.Fprintf(w, "%d bytes from %s: icmp_seq=%d time=%v ttl=%v (DUP!)\n",
				pkt.Nbytes, pkt.IPAddr, pkt.Seq, pkt.Rtt, pkt.Ttl)
		}
		pinger.OnFinish = func(stats *ping.Statistics) {
			_, _ = fmt.Fprintf(w, "\n--- %s ping statistics ---\n", stats.Addr)
			_, _ = fmt.Fprintf(w, "%d packets transmitted, %d packets received, %v%% packet loss\n",
				stats.PacketsSent, stats.PacketsRecv, stats.PacketLoss)
			_, _ = fmt.Fprintf(w, "round-trip min/avg/max/stddev = %v/%v/%v/%v\n",
				stats.MinRtt, stats.AvgRtt, stats.MaxRtt, stats.StdDevRtt)
		}
		_, _ = fmt.Fprintf(w, "PING %s (%s):\n",
			pinger.Addr(), pinger.IPAddr())

		err = pinger.Run()
		if err != nil {
			Logger.Errorf("could not create pinger 91.234.180.53")
		}*/
}

func join(p url.Values) string {
	kvkv := make([]string, 0, 20)
	for k, vv := range p {
		if len(vv) == 0 {
			kvkv = append(kvkv, k+"=")
		} else if len(vv) == 1 {
			kvkv = append(kvkv, k+"="+vv[0])
		} else {
			for _, v := range vv {
				kvkv = append(kvkv, k+"="+v)
			}
		}
	}
	return strings.Join(kvkv, ",")
}

func (s *webSrv) calculate(yyyy string, mm string, number string, prevStr string, currStr string, debtStr string,
	priceStr string, coefStr string, fio string) (sum string, purpose string) {

	year, err := strconv.Atoi(yyyy)
	if err != nil || year > 2050 || year < 2022 {
		year = 0
	}
	month, err := strconv.Atoi(mm)
	if err != nil || month > 12 || month < 1 {
		month = 0
	}
	if year == 0 && month == 0 {
		year = time.Now().In(Location).Year()
		month = int(time.Now().In(Location).Month()) - 1
		if month == 0 {
			month = 12
			year--
		}
	}
	if year == 0 || month == 0 {
		return "", ""
	}
	prev, err := strconv.ParseFloat(prevStr, 64)
	if err != nil {
		return "", ""
	}
	curr, err := strconv.ParseFloat(currStr, 64)
	if err != nil {
		return "", ""
	}
	debt := 0.0
	if len(debtStr) > 0 {
		debt, err = strconv.ParseFloat(debtStr, 64)
		if err != nil {
			return "", ""
		}
	}
	var price float64
	if len(priceStr) == 0 {
		price = s.priceHist.coef(year, month)
		priceStr = fmt.Sprintf("%.2f", price)
	} else {
		price, err = strconv.ParseFloat(priceStr, 64)
		if err != nil {
			price = 0
			Logger.Error("error parsing price %s %v", s.priceHist, err)
		}
	}
	var coef float64
	if len(coefStr) == 0 {
		coef = s.coefHist.coef(year, month)
		coefStr = fmt.Sprintf("%.2f", coef)
	} else {
		coef, err = strconv.ParseFloat(coefStr, 64)
		if err != nil {
			coef = 0
			Logger.Error("error parsing coef %s %v", coefStr, err)
		}
	}
	coefMult := 1 + 0.01*coef
	sum = fmt.Sprintf("%.2f", debt+(curr-prev)*price*coefMult)
	mnt := []string{"янв", "фев", "мар", "апр", "май", "июн", "июл", "авг", "сен", "окт", "ноя", "дек"}[month-1]

	debtText := ""
	if debt != 0 {
		debtText = fmt.Sprintf("%.2f + ", debt)
	}
	replacer := strings.NewReplacer(
		"{mnt}", mnt,
		"{year}", strconv.Itoa(year),
		"{fio}", fio,
		"{number}", number,
		"{debt}", debtText,
		"{curr}", fmt.Sprintf("%.2f", curr),
		"{prev}", fmt.Sprintf("%.2f", prev),
		"{price}", priceStr,
		"{coef}", fmt.Sprintf("%.4f", coefMult),
		"{sum}", sum)

	purpose = replacer.Replace("За эл-энергию, {mnt} {year}, {fio} участок {number}, {debt}({curr} - {prev})x{price}x{coef} :: {sum}")
	return sum, purpose
}

func (s *webSrv) writeImage(w http.ResponseWriter, sum, purpose, lastName string) {
	qr := QRHeader
	for k, v := range s.QRElements {
		qr += "|" + k + "=" + v
	}
	qr += "|" + QRNamePurpose + "=" + purpose
	if len(lastName) != 0 {
		qr += "|" + QRNameLastName + "=" + lastName
	}
	{
		summa, _ := strconv.ParseFloat(sum, 64)
		qr += "|" + QRNameSum + "=" + fmt.Sprintf("%.0f", summa*100)
	}
	imgBytes, err := qrcode.Encode(qr, qrcode.Medium, 256)
	if err != nil {
		Logger.Errorf("error encoding qr code: %v", err)
		http.Error(w, fmt.Sprintf("500 error encoding qr code: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	io.Copy(w, bytes.NewReader(imgBytes))
}

func (s *webSrv) generateTOTPQRCodeImage(w http.ResponseWriter, phone string) {
	c, ok := s.gate.CallStore.Get("+7" + phone)
	trueQR := false
	if ok {
		d := time.Since(c.time)
		trueQR = c.cnt == 0 && d <= 30*time.Second
		Logger.Infof("generate N %d TOTP QR: call %s was %ds ago", c.cnt, "+7"+phone, d/time.Second)
		s.gate.CallStore.Increment("+7" + phone)
	} else {
		Logger.Infof("generate TOTP QR: call %s not found", "+7"+phone)
	}
	salt := ""
	if trueQR {
		salt = "SNT_Semislavka"
	} else {
		salt = strconv.FormatInt(time.Now().UnixNano()%1_000_000_000_000, 10)
	}
	h := sha1.New()
	h.Write([]byte(phone + salt))
	hashBytes := h.Sum(nil)
	hashStr := hex.EncodeToString(hashBytes)
	secret := hashStr[:16]

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "СНТ Семиславка", // Название компании/приложения
		AccountName: phone,            // Имя аккаунта (номер телефона)
		Secret:      []byte(secret),   // Наш секрет из 16 символов
	})
	if err != nil {
		Logger.Errorf("Ошибка при создании QR: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	var png []byte
	png, err = qrcode.Encode(key.URL(), qrcode.Medium, 256)
	if err != nil {
		Logger.Errorf("Ошибка при создании QR: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	Logger.Infof("%v QR for +7%s generated", trueQR, phone)
	w.Header().Set("Content-Type", "image/png")
	w.Write(png)
}

func (s *webSrv) servePayTemplate(w http.ResponseWriter, r *http.Request) {
	fp := filepath.Join(s.staticDir, filepath.Clean(r.URL.Path), "index.html")

	/*	// Return a 404 if the template doesn't exist
		info, err := os.Stat(fp)
		if err != nil {
			if os.IsNotExist(err) {
				http.NotFound(w, r)
				return
			}
		}
		// Return a 404 if the request is for a directory
		if info.IsDir() {
			http.NotFound(w, r)
			return
		}*/
	tmpl, err := template.ParseFiles(fp)
	if err != nil {
		Logger.Error(err)
		http.Error(w, http.StatusText(500), 500)
		return
	}
	query := r.URL.Query()
	sum := query.Get(paramNameSum)
	purpose := query.Get(paramNamePurpose)
	fio := query.Get(paramNameFio)
	formHtml := `<form action="/docs/оплата/" method="post">
    Сумма:<input type="text" name="{sum}" size="10" value="{sum_val}">
    ФИО:<input type="text" name="{fio}" value="{fio_val}" size="50">
    Назначение&nbsp;перевода:<input type="text" name="{purpose}" value="{purpose_val}" size="50">
    <input type="submit" value="Ввод">
</form>`
	replacer := strings.NewReplacer(
		"{sum}", paramNameSum,
		"{sum_val}", sum,
		"{purpose}", paramNamePurpose,
		"{purpose_val}", purpose,
		"{fio}", paramNameFio,
		"{fio_val}", fio,
	)
	formHtml = replacer.Replace(formHtml)

	params := url.Values{}
	params.Add(paramNameSum, sum)
	params.Add(paramNamePurpose, purpose)
	params.Add(paramNameFio, fio)

	urlLine := fmt.Sprintf(`<p><img src="%s?%s" alt="Not so big"></p>`,
		qrImgPath, params.Encode())
	tdata := tmplFormData{
		Form:   template.HTML(formHtml),
		ImgURL: template.HTML(urlLine),
	}
	w2 := newWriterInterceptor(w)
	err = tmpl.Execute(w2, &tdata)
	if err != nil {
		log.Print(err.Error())
		http.Error(w, http.StatusText(500), 500)
	} else {
		html := w2.buf.String()
		for _, l := range strings.Split(html, "\n") {
			if strings.Contains(l, ".jpeg") {
				Logger.Debug(l)
			}
		}
	}
}

func (s *webSrv) servePayElectrTemplate(w http.ResponseWriter, r *http.Request) {
	fp := filepath.Join(s.staticDir, filepath.Clean(r.URL.Path), "index.html")

	/*	// Return a 404 if the template doesn't exist
		info, err := os.Stat(fp)
		if err != nil {
			if os.IsNotExist(err) {
				http.NotFound(w, r)
				return
			}
		}
		// Return a 404 if the request is for a directory
		if info.IsDir() {
			http.NotFound(w, r)
			return
		}*/
	tmpl, err := template.ParseFiles(fp)
	if err != nil {
		Logger.Error(err)
		http.Error(w, http.StatusText(500), 500)
		return
	}
	query := r.URL.Query()
	year := query.Get(paramNameYear)
	month := query.Get(paramNameMonth)
	number := query.Get(paramNameNumber)
	prev := query.Get(paramNamePrevElectr)
	curr := query.Get(paramNameCurrElectr)
	debt := query.Get(paramNameDebt)
	fio := query.Get(paramNameFio)
	price := query.Get(paramNamePrice)
	if len(price) == 0 {
		price = fmt.Sprintf("%.2f", s.priceHist.coefStr(year, month))
	}
	coef := query.Get(paramNameCoef)
	if len(coef) == 0 {
		coef = fmt.Sprintf("%.2f", s.coefHist.coefStr(year, month))
	}
	if len(year) != 0 && len(month) != 0 && len(number) != 0 && (len(prev) == 0 || len(curr) == 0 || len(debt) == 0) {
		ev := s.loadFromFile(year, month, number)
		if ev != nil {
			if len(prev) == 0 {
				prev = ev.PrevEvidence
			}
			if len(curr) == 0 {
				curr = ev.CurrEvidence
			}
			if len(debt) == 0 {
				debt = ev.prepaidMinusDebtAsStr()
			}
		}
	}
	formHtml := `<form action="/docs/оплата-эл/" method="post">
    Год:<input type="text" name="{yyyy}" size="4" value="{yyyy_val}">
    Месяц:<input type="text" name="{mm}" size="2" value="{mm_val}">
    Номер&nbsp;участка:<input type="text" name="{n}" value="{n_val}" size="3">
    <input type="hidden" name="{prevyyyymmnumber}" value="{prevyyyymmnumber_val}">
    Предыдущее&nbsp;показание:<input type="text" name="{prev}" value="{prev_val}" size="6">
    Текущее&nbsp;показание:<input type="text" name="{curr}" value="{curr_val}" size="6">
    Долг:<input type="text" name="{debt}" value="{debt_val}" size="8">
    ФИО:<input type="text" name="{fio}" value="{fio_val}" size="15">
    Тариф:<input type="text" name="{price}" value="{price_val}" size="4">
    Процент&nbsp;потерь:<input type="text" name="{coef}" value="{coef_val}" size="5">
    <input type="submit" value="Ввод">
</form>`

	replacer := strings.NewReplacer(
		"{yyyy}", paramNameYear,
		"{yyyy_val}", year,
		"{mm}", paramNameMonth,
		"{mm_val}", month,
		"{prev}", paramNamePrevElectr,
		"{prev_val}", prev,
		"{curr}", paramNameCurrElectr,
		"{curr_val}", curr,
		"{debt}", paramNameDebt,
		"{debt_val}", debt,
		"{fio}", paramNameFio,
		"{fio_val}", fio,
		"{n}", paramNameNumber,
		"{n_val}", number,
		"{prevyyyymmnumber}", paramNamePrevKey,
		"{price}", paramNamePrice,
		"{price_val}", price,
		"{coef}", paramNameCoef,
		"{coef_val}", coef,
		"{prevyyyymmnumber_val}", fmt.Sprintf("%04s%02s", year, month),
	)
	formHtml = replacer.Replace(formHtml)

	params := url.Values{}
	params.Add(paramNameYear, year)
	params.Add(paramNameMonth, month)
	params.Add(paramNameNumber, number)
	params.Add(paramNamePrevElectr, prev)
	params.Add(paramNameCurrElectr, curr)
	params.Add(paramNameDebt, debt)
	params.Add(paramNameFio, fio)
	params.Add(paramNamePrice, price)
	params.Add(paramNameCoef, coef)
	urlLine := fmt.Sprintf(`<p><img src="%s?%s" alt="Not so big"></p>`,
		qreImgPath, params.Encode())
	tdata := &tmplFormData{
		Form:   template.HTML(formHtml),
		ImgURL: template.HTML(urlLine),
	}

	sum, purpose := s.calculate(year, month, number, prev, curr, debt, price, coef, fio)

	if len(sum) != 0 || len(purpose) != 0 {
		tdata.FormFooter = template.HTML(fmt.Sprintf("Назначение платежа: <em>%s</em><br>Сумма: <em>%s</em><br>", purpose, sum))
	}
	w2 := newWriterInterceptor(w)
	err = tmpl.Execute(w2, tdata)
	if err != nil {
		log.Print(err.Error())
		http.Error(w, http.StatusText(500), 500)
	} else {
		html := w2.buf.String()
		for _, l := range strings.Split(html, "\n") {
			//if strings.Contains(l, "<img ") || strings.Contains(l, "<img>") {
			if strings.Contains(l, qrImgPath) || strings.Contains(l, qreImgPath) {
				Logger.Debug(l)
			}
		}
	}
}

type tmplQRData struct {
	Purpose template.HTML
	ImgURL  template.HTML
}

func (s *webSrv) serveQRTemplate(w http.ResponseWriter, r *http.Request) {
	fp := filepath.Join(s.staticDir, filepath.Clean(r.URL.Path), "index.html")
	tmpl, err := template.ParseFiles(fp)
	if err != nil {
		Logger.Error(err)
		http.Error(w, http.StatusText(500), 500)
		return
	}
	tdata := &tmplQRData{}
	query := r.URL.Query()
	year := query.Get(paramNameYear)
	month := query.Get(paramNameMonth)
	number := query.Get(paramNameNumber)
	hash := query.Get(paramNameHash)

	Logger.Infof("QR code: %8s %s%s ip=%s, UserAgent=%s",
		number, year, month, remoteIP(r), r.UserAgent())

	params := url.Values{}
	params.Add(paramNameYear, year)
	params.Add(paramNameMonth, month)
	params.Add(paramNameNumber, number)
	params.Add(paramNameHash, hash)

	var ok bool
	if checkHash(year, month, number, hash) {
		tdata.Purpose, _, ok = s.purpose(year, month, number)
	} else {
		tdata.Purpose = "неверная ссылка"
	}
	url := ""
	if ok {
		url = fmt.Sprintf(`%s?%s`, qrcImgPath, params.Encode())
	} else {
		i := rand.Intn(173) + 1
		ext := "png"
		switch i {
		case 3, 14:
			ext = "gif"
		case 1, 7, 9, 10, 12:
			ext = "jpg"
		}
		if i < 100 {
			url = fmt.Sprintf("/images/qr%02d.%s", i, ext)
		} else {
			url = fmt.Sprintf("/images/qr%d.%s", i, ext)
		}
	}
	Logger.Debugf(url)
	tdata.ImgURL = template.HTML(fmt.Sprintf(`<p><img src="%s" alt="Not so big" width="350"></p>`, url))
	err = tmpl.Execute(w, tdata)
	if err != nil {
		log.Print(err.Error())
		http.Error(w, http.StatusText(500), 500)
		/*

		   ./public/images/qr07.jpeg
		    ./public/images/qr01.jpg
		   ./public/images/qr09.jpg
		   ./public/images/qr10.jpg
		   ./public/images/qr12.jpg

		*/
	}
}
func (s *webSrv) purpose(year string, month string, number string) (template.HTML, float64, bool) {
	ev := s.loadFromFile(year, month, number)
	if ev == nil {
		return "файл не найден", 0, false
	}
	purpose := ""
	prev, err := strconv.ParseFloat(ev.PrevEvidence, 64)
	if err != nil {
		prev = 0
	}
	curr, err := strconv.ParseFloat(ev.CurrEvidence, 64)
	if err != nil {
		curr = 0
	}
	price := s.priceHist.coefStr(year, month)
	coef := s.coefHist.coefStr(year, month)
	debt := ev.prepaidMinusDebt()
	totalCalc := (curr - prev) * price * (1 + 0.01*coef)
	currDebtCalc := debt + totalCalc
	currDebt, err := strconv.ParseFloat(ev.CurrDebt, 64)
	if err == nil && currDebtCalc-currDebt >= 1 {
		purpose += "<b>Внимание! Рассчитанная сумма расходится  с ведомостью!  <b>"
	}
	debtText := ""
	if debt != 0 {
		debtText = fmt.Sprintf("%.2f + ", debt)
	}
	replacer := strings.NewReplacer(
		"{mnt}", month,
		"{year}", year,
		"{number}", number,
		"{debt}", debtText,
		"{curr}", fmt.Sprintf("%.2f", curr),
		"{prev}", fmt.Sprintf("%.2f", prev),
		"{price}", fmt.Sprintf("%.2f", s.priceHist.coefStr(year, month)),
		"{coef}", fmt.Sprintf("%.2f", s.coefHist.coefStr(year, month)),
		"{sum}", fmt.Sprintf("%.2f", currDebtCalc),
	)
	purpose += replacer.Replace(
		"За эл-энергию, {mnt} {year}, участок {number}, {debt}({curr} - {prev})x{price}x{coef} :: {sum}")

	return template.HTML(purpose), currDebt, true
}

func checkHash(year string, month string, number string, hash string) bool {
	if len(year) == 0 || len(month) == 0 || len(number) == 0 || len(hash) == 0 {
		return false
	}
	h := sha1Hash(year, month, number)
	return hash == h
}

func (s *webSrv) serveTemplate(w http.ResponseWriter, r *http.Request, tdata any,
	tmplPreprocessor func(s string) string) {

	fp := filepath.Join(s.staticDir, filepath.Clean(r.URL.Path), "index.html")
	/*	tmpl, err := template.ParseFiles(fp)
		if err != nil {
			Logger.Error(err)
			http.Error(w, http.StatusText(500), 500)
			return
		}
	*/
	name := filepath.Base(fp)
	bb, err := os.ReadFile(fp)
	if err != nil {
		Logger.Error(err)
		http.Error(w, http.StatusText(500), 500)
		return
	}
	text := string(bb)
	if tmplPreprocessor != nil {
		text = tmplPreprocessor(text)
	}
	tmpl, err := template.New(name).Parse(text)
	if err != nil {
		Logger.Error(err)
		http.Error(w, http.StatusText(500), 500)
		return
	}
	w2 := newWriterInterceptor(w)
	err = tmpl.Execute(w2, tdata)
	if err != nil {
		log.Print(err.Error())
		http.Error(w, http.StatusText(500), 500)
	}
}

func (s *webSrv) loadFromFile(year string, month string, number string) *ElectrEvidence {
	y, err := strconv.Atoi(year)
	if err != nil {
		Logger.Errorf("expected 4-digits year %s %v", year, err)
		return nil
	}
	m, err := strconv.Atoi(month)
	if err != nil {
		Logger.Errorf("expected 2-digits month %s %v", month, err)
		return nil
	}
	items := LoadElectrForMonth(s.staticDir, y, m)
	ev := toMap(items)[number]
	return ev
}

func (s *webSrv) FindByEmailPrefix(email string) map[string]bool {
	email = cleanEmail(email)
	plotNumbers := make(map[string]bool)
	registry := s.registry.Load().(*Registry)
	for _, r := range registry.searchResult.Records {
		if len(r.PlotNumber) == 0 {
			continue
		}
		if strings.HasPrefix(cleanEmail(r.Email), email) {
			plotNumbers[r.PlotNumber] = true
		}
	}
	for _, r := range registry.registry {
		if len(r.PlotNumber) == 0 {
			continue
		}
		if strings.HasPrefix(cleanEmail(r.Email), email) {
			plotNumbers[r.PlotNumber] = true
		}
	}
	return plotNumbers
}

type tmplFormData struct {
	Form       template.HTML
	FormFooter template.HTML
	ImgURL     template.HTML
}

func newWriterInterceptor(w io.Writer) *writerInterceptor {
	return &writerInterceptor{target: w}
}

type writerInterceptor struct {
	buf    bytes.Buffer
	target io.Writer
}

func (w *writerInterceptor) Write(p []byte) (n int, err error) {
	n, err = w.target.Write(p)
	if err == nil {
		w.buf.Write(p[:n])
	}
	return
}

func remoteIP(r *http.Request) string {
	xff := r.Header.Get("X-Forwarded-For")
	if len(xff) != 0 {
		ips := strings.Split(xff, ", ")
		return ips[0]
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	return ip
}

func (ws *webSrv) loadSntClubUsers() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://file.sntclub.ru/upload/downloadfiles/5a6/45y97kpixruiqkj6fkigsqxpo571e24m/Registry_people_07-04-2026.xlsx", nil)

	if err != nil {
		Logger.Errorf("%v", err)
		return
	}
	req.Header.Add("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36")
	req.Header.Add("Referer", "https://lk.sntclub.ru/")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		Logger.Errorf("sntclub people registry http: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		Logger.Infof("sntclub people registry http %d", resp.StatusCode)
		return
	}
	xlsxFile, err := excelize.OpenReader(resp.Body)
	if err != nil {
		Logger.Errorf("sntclub people registry excel reader: %v", err)
		return
	}
	defer func() { _ = xlsxFile.Close() }()

	sheetName := xlsxFile.GetSheetName(0)
	if sheetName == "" {
		Logger.Debugf("sntclub people registry excel reader: sheet not found")
		return
	}
	rows, err := xlsxFile.GetRows(sheetName)
	if err != nil {
		Logger.Errorf("sntclub people registry excel reader: %v", err)
		return
	}
	fname := filepath.Join(ws.dataDir, "sntclub_users.csv")
	csvFile, err := os.Create(fname)
	if err != nil {
		Logger.Errorf("creating %s: %v", fname, err)
		return
	}
	defer csvFile.Close()

	writer := csv.NewWriter(csvFile)
	defer writer.Flush()

	// Записываем каждую строку в CSV
	for i, row := range rows {
		if err := writer.Write(row); err != nil {
			Logger.Errorf("writing %d row to %s: %v", i, fname, err)
			return
		}
	}
}
