package tgsrv

import (
	"bytes"
	"context"
	"crypto/sha1"
	"fmt"
	"github.com/gocarina/gocsv"
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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"
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

	site = "https://semislavka.win"

	QRSalt = "ee1e4a719ddac2689"

	// required
	QRHeader          = "ST00011"
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

func init() {
	rand.Seed(time.Now().Unix())
}

func StartWebServer(port int, staticDir, dir string, QRElements map[string]string, price string, coef map[string]float64, abort chan struct{}, pinger *pingMonitor) *webSrv {
	webServer := newWebServer(port, staticDir, dir, QRElements, price, coef, pinger)
	webServer.start(port)
	srv := webServer.httpServer
	go func() {
		<-abort
		srv.Shutdown(context.Background())
	}()
	return webServer
}

func newWebServer(port int, staticDir string, dir string, QRElements map[string]string, price string,
	coef map[string]float64, pinger *pingMonitor) *webSrv {

	ws := new(webSrv)
	ws.price = price
	ws.QRElements = QRElements
	ws.staticDir = staticDir
	ws.dataDir = dir
	ws.pinger = pinger
	(&ws.coefHist).fromMap(coef)

	fs := http.FileServer(http.Dir(staticDir))
	//ws.staticHandler = http.StripPrefix("/static/", fs)
	ws.staticHandler = fs

	ws.registry = loadRegistry(dir)

	http.HandleFunc("/", ws.handle)

	ws.httpServer = &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: nil}
	return ws
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
	price         string
	coefHist      valueDates
	QRElements    map[string]string
	staticDir     string
	dataDir       string
	staticHandler http.Handler
	httpServer    *http.Server
	pinger        *pingMonitor
	registry      *Registry
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
			it.QRURL = QRURL(year, month, it.PlotNumber)
		}
		gocsv.Marshal(items, w)
	}

	if r.URL.Path == internetDocsPath {
		s.serveTemplate(w, r, Bool(false), func(s string) string {
			return strings.Replace(s, "<script ", "{{end}}\n <script ", 1)
		})
		return
	}
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
	fmt.Fprintf(w, string(out))
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
	if len(priceStr) == 0 {
		priceStr = s.price
	}
	price, err := strconv.ParseFloat(priceStr, 64)
	if err != nil {
		price = 0
		Logger.Error("error parsing price %s %v", s.price, err)
	}
	var coef float64
	if len(coefStr) == 0 {
		coef = s.coefHist.coef(year, month)
		coefStr = fmt.Sprintf("%.1f", coef)
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
		"{price}", s.price,
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
		price = s.price
	}
	coef := query.Get(paramNameCoef)
	if len(coef) == 0 {
		coef = fmt.Sprintf("%.1f", s.coefHist.coefStr(year, month))
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
		if i == 3 || i == 14 {
			ext = "gif"
		} else if i == 1 || i == 7 || i == 9 || i == 10 || i == 12 {
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
	price, _ := strconv.ParseFloat(s.price, 64)
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
		"{price}", s.price,
		"{coef}", fmt.Sprintf("%.1f", s.coefHist.coefStr(year, month)),
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

func sha1Hash(year string, month string, number string) string {
	hasher := sha1.New()
	io.WriteString(hasher, year)
	io.WriteString(hasher, month)
	io.WriteString(hasher, number)
	io.WriteString(hasher, QRSalt)
	h := fmt.Sprintf("%x", hasher.Sum(nil))
	return h
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
