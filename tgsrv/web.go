package tgsrv

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"
)

const (
	paramNameSum        = "sum"
	paramNameYear       = "yyyy"
	paramNameMonth      = "mm"
	paramNamePrevElectr = "prev"
	paramNameCurrElectr = "curr"
	paramNameDebt       = "debt"
	paramNameFio        = "fio"
	paramNameNumber     = "n"
	paramNamePrevKey    = "prevyyyymmnumber"
	paramNamePurpose    = "purpose"
	paramNamePrice      = "price"
	paramNameCoef       = "coef"
	qrPath              = "/images/qr.jpg"
	qrePath             = "/images/qre.jpg"
	payPath             = "/docs/оплата/"
	payElectrPath       = "/docs/оплата-эл/"
	contactsPath        = "/docs/contacts/"
	internetPath        = "/docs/internet/"
)
const (
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

var Location *time.Location

func init() {
	var err error
	Location, err = time.LoadLocation("Europe/Moscow")
	if err != nil {
		log.Fatal(err)
	}
}

func StartWebServer(port int, staticDir string, QRElements map[string]string, price, coef string,
	abort chan struct{}, pinger *pingMonitor) *http.Server {

	webServer := newWebServer(port, staticDir, QRElements, price, coef, pinger)
	webServer.start(port)
	srv := webServer.httpServer
	go func() {
		<-abort
		srv.Shutdown(context.Background())
	}()
	return srv
}

func newWebServer(port int, staticDir string, QRElements map[string]string, price, coef string,
	pinger *pingMonitor) *webSrv {

	ws := new(webSrv)
	ws.price = price
	ws.coef = coef
	ws.QRElements = QRElements
	ws.staticDir = staticDir
	ws.pinger = pinger

	fs := http.FileServer(http.Dir(staticDir))
	//ws.staticHandler = http.StripPrefix("/static/", fs)
	ws.staticHandler = fs
	http.HandleFunc("/", ws.handle)

	ws.httpServer = &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: nil}
	return ws
}

type webSrv struct {
	price         string
	coef          string
	QRElements    map[string]string
	staticDir     string
	staticHandler http.Handler
	httpServer    *http.Server
	pinger        *pingMonitor
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
		query := r.URL.Query()
		s.writeImage(w,
			query.Get(paramNameSum),
			query.Get(paramNamePurpose),
			query.Get(paramNameFio),
		)
	}
	if r.URL.Path == qrePath {
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
	if r.URL.Path == "/" || r.URL.Path == contactsPath {
		type tdataType struct {
			DivAlignRight template.HTML
			DivEnd        template.HTML
		}
		tdata := &tdataType{
			DivAlignRight: template.HTML(`<div style="text-align: right">`),
			DivEnd:        template.HTML(`</div>`),
		}
		s.serveTemplate(w, r, tdata)
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
			s.serveTemplate(w, r, tdata)
			return
		}
		var buf bytes.Buffer
		pingIp(&buf, s.pinger.bestIP())
		tdata.PingResult = template.HTML(buf.String())
		if query.Get("ping") == "2" {
			tdata.PingResult += "\n\n"
			ips := s.pinger.IPs()
			sort.Sort(byIP(ips))
			for _, ip := range ips {
				tdata.PingResult += template.HTML(ip + "\n")
			}
		}
		s.serveTemplate(w, r, tdata)
		return
	}
	s.staticHandler.ServeHTTP(w, r)
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

	if len(coefStr) == 0 {
		coefStr = s.coef
	}
	coef, err := strconv.ParseFloat(coefStr, 64)
	if err != nil {
		coef = 0
		Logger.Error("error parsing coef %s %v", s.coef, err)
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

	urlLine := fmt.Sprintf(`<p><img src="/images/qr.jpg?%s" alt="Not so big"></p>`, params.Encode())
	tdata := tmplData{
		Form:  template.HTML(formHtml),
		QRImg: template.HTML(urlLine),
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
		coef = s.coef
	}
	if len(year) != 0 && len(month) != 0 && len(number) != 0 && (len(prev) == 0 || len(curr) == 0 || len(debt) == 0) {
		ev, err := s.loadFromFile(year, month, number)
		if err != nil {
			Logger.Error(err)
		} else if ev != nil {
			if len(prev) == 0 {
				prev = ev.PrevEvidence
			}
			if len(curr) == 0 {
				curr = ev.CurrEvidence
			}
			if len(debt) == 0 {
				debt = ev.prepaidMinusDebt()
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
	urlLine := fmt.Sprintf(`<p><img src="/images/qre.jpg?%s" alt="Not so big"></p>`, params.Encode())
	tdata := &tmplData{
		Form:  template.HTML(formHtml),
		QRImg: template.HTML(urlLine),
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
			if strings.Contains(l, qrPath) || strings.Contains(l, qrePath) {
				Logger.Debug(l)
			}
		}
	}
}

func (s *webSrv) serveTemplate(w http.ResponseWriter, r *http.Request, tdata any) {
	fp := filepath.Join(s.staticDir, filepath.Clean(r.URL.Path), "index.html")
	tmpl, err := template.ParseFiles(fp)
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

func (s *webSrv) loadFromFile(year string, month string, number string) (*ElectrEvidence, error) {
	y, err := strconv.Atoi(year)
	if err != nil {
		return nil, err
	}
	m, err := strconv.Atoi(month)
	if err != nil {
		return nil, err
	}
	ev := LoadElectrForMonth(s.staticDir, y, m)[number]
	return ev, nil
}

type tmplData struct {
	Form       template.HTML
	FormFooter template.HTML
	QRImg      template.HTML
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
