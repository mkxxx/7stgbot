package tgsrv

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
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
	paramNamePurpose    = "purpose"
	qrPath              = "/images/qr.jpg"
	qrePath             = "/images/qre.jpg"
	payPath             = "/docs/оплата/"
	payElectrPath       = "/docs/оплата-эл/"
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

func StartWebServer(port int, staticDir string, QRElements map[string]string, price, coef string) *http.Server {
	webServer := newWebServer(port, staticDir, QRElements, price, coef)
	webServer.start(port)
	return webServer.httpServer
}

func newWebServer(port int, staticDir string, QRElements map[string]string, price, coef string) *webSrv {
	ws := new(webSrv)
	ws.price = price
	ws.coef = coef
	ws.QRElements = QRElements
	ws.staticDir = staticDir
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
			params := url.Values{}
			params.Add(paramNameYear, r.FormValue(paramNameYear))
			params.Add(paramNameMonth, r.FormValue(paramNameMonth))
			params.Add(paramNamePrevElectr, r.FormValue(paramNamePrevElectr))
			params.Add(paramNameCurrElectr, r.FormValue(paramNameCurrElectr))
			params.Add(paramNameDebt, r.FormValue(paramNameDebt))
			params.Add(paramNameFio, r.FormValue(paramNameFio))
			params.Add(paramNameNumber, r.FormValue(paramNameNumber))
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
			query.Get(paramNamePurpose))
	}
	if r.URL.Path == qrePath {
		query := r.URL.Query()
		sum, purpose := s.calculate(query)
		s.writeImage(w, sum, purpose)
		return
	}
	s.staticHandler.ServeHTTP(w, r)
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

func (s *webSrv) calculate(query url.Values) (sum string, purpose string) {
	year, err := strconv.Atoi(query.Get(paramNameYear))
	if err != nil || year > 2050 || year < 2022 {
		year = 0
	}
	month, err := strconv.Atoi(query.Get(paramNameMonth))
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
	prev, err := strconv.Atoi(query.Get(paramNamePrevElectr))
	if err != nil {
		return "", ""
	}
	curr, err := strconv.Atoi(query.Get(paramNameCurrElectr))
	if err != nil {
		return "", ""
	}
	debt := 0.0
	debtText := query.Get(paramNameDebt)
	if len(debtText) > 0 {
		debt, err = strconv.ParseFloat(debtText, 64)
		if err != nil {
			return "", ""
		}
	}
	fio := query.Get(paramNameFio)
	number := query.Get(paramNameNumber)

	price, err := strconv.ParseFloat(s.price, 64)
	if err != nil {
		price = 0
		Logger.Error("error parsing price %s %v", s.price, err)
	}
	coef, err := strconv.ParseFloat(s.coef, 64)
	if err != nil {
		coef = 0
		Logger.Error("error parsing coef %s %v", s.coef, err)
	}
	sum = fmt.Sprintf("%.2f", debt+float64(curr-prev)*price*coef)
	mnt := []string{"янв", "фев", "мар", "апр", "май", "июн", "июл", "авг", "сен", "окт", "ноя", "дек"}[month-1]
	if debt != 0 {
		purpose = fmt.Sprintf("За эл-энергию, %s %d, %s участок %s, %.2f + (%d - %d)x%sx%s = %s",
			mnt, year, fio, number, debt, curr, prev, s.price, s.coef, sum)
	} else {
		purpose = fmt.Sprintf("За эл-энергию, %s %d, %s участок %s, (%d - %d)x%sx%s = %s",
			mnt, year, fio, number, curr, prev, s.price, s.coef, sum)
	}
	return sum, purpose
}

func (s *webSrv) writeImage(w http.ResponseWriter, sum string, purpose string) {
	qr := QRHeader
	for k, v := range s.QRElements {
		qr += "|" + k + "=" + v
	}
	qr += "|" + QRNamePurpose + "=" + purpose
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
	formHtml := `<form action="/docs/оплата/" method="post">
    Сумма:<input type="text" name="sum" size="10" value=%q>
    Назначение перевода:<input type="text" name="purpose" value=%q size="50">
    <input type="submit" value="Ввод">
</form>`
	formHtml = fmt.Sprintf(formHtml, sum, purpose)

	params := url.Values{}
	params.Add(paramNameSum, sum)
	params.Add(paramNamePurpose, purpose)

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
	prev := query.Get(paramNamePrevElectr)
	curr := query.Get(paramNameCurrElectr)
	debt := query.Get(paramNameDebt)
	fio := query.Get(paramNameFio)
	number := query.Get(paramNameNumber)
	formHtml := `<form action="/docs/оплата-эл/" method="post">
    Год:<input type="text" name="yyyy" size="4" value=%q>
    Месяц:<input type="text" name="mm" size="2" value=%q>
    Предыдущее показание:<input type="text" name="prev" value=%q size="6">
    Текущее показание:<input type="text" name="curr" value=%q size="6">
    Долг:<input type="text" name="debt" value=%q size="8">
    ФИО:<input type="text" name="fio" value=%q size="15">
    Номер участка:<input type="text" name="n" value=%q size="3">
    <input type="submit" value="Ввод">
</form>`

	formHtml = fmt.Sprintf(formHtml, year, month, prev, curr, debt, fio, number)

	params := url.Values{}
	params.Add(paramNameYear, year)
	params.Add(paramNameMonth, month)
	params.Add(paramNamePrevElectr, prev)
	params.Add(paramNameCurrElectr, curr)
	params.Add(paramNameDebt, debt)
	params.Add(paramNameFio, fio)
	params.Add(paramNameNumber, number)
	urlLine := fmt.Sprintf(`<p><img src="/images/qre.jpg?%s" alt="Not so big"></p>`, params.Encode())
	tdata := &tmplData{
		Form:  template.HTML(formHtml),
		QRImg: template.HTML(urlLine),
	}
	sum, purpose := s.calculate(query)
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
