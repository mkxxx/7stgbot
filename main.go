package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"time"

	"github.com/BurntSushi/toml"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/skip2/go-qrcode"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var numericKeyboard = tgbotapi.NewReplyKeyboard(
	tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("1"),
		tgbotapi.NewKeyboardButton("2"),
		tgbotapi.NewKeyboardButton("3"),
	),
	tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("4"),
		tgbotapi.NewKeyboardButton("5"),
		tgbotapi.NewKeyboardButton("6"),
	),
)
var logger *zap.SugaredLogger
var (
	cfgPath string
	logfile string
	port    int
)

func main() {
	flag.StringVar(&logfile, "logfile", "7stgbot.log", "log file")
	flag.IntVar(&port, "port", 80, "web server port")
	flag.StringVar(&cfgPath, "cfg", "./config.toml", "Path to config file in toml format")
	flag.Parse()

	zap.NewDevelopmentConfig()

	// lumberjack.Logger is already safe for concurrent use, so we don't need to
	// lock it.
	w := zapcore.AddSync(&lumberjack.Logger{
		Filename:   logfile,
		MaxSize:    50, // megabytes
		MaxBackups: 0,
		MaxAge:     0, // days
	})
	w = zapcore.NewMultiWriteSyncer(w, os.Stderr)
	encoderCfg := zap.NewDevelopmentEncoderConfig()
	timeEncoder := zapcore.TimeEncoderOfLayout("2006 - 01 - 02_15:04:05.000")
	encoderCfg.EncodeTime = func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
		timeEncoder(t.In(time.Local), enc)
	}
	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoderCfg),
		w,
		zap.DebugLevel,
	)
	logger = zap.New(core).Sugar()
	defer logger.Sync()

	log.SetOutput(w)

	var cfg Config
	if _, err := toml.DecodeFile(cfgPath, &cfg); err != nil {
		logger.Errorf("error parsing toml config by path %q: %v", cfgPath, err)
		flag.PrintDefaults()
		return
	}
	cfg.QR["Sum"] = "0"
	cfg.QR["Purpose"] = ""
	qr := "ST00011"
	for k, v := range cfg.QR {
		qr += "|" + k + "=" + v
	}
	//qrcode.WriteFile(qr, qrcode.Medium, 256, "qr.png")

	http.HandleFunc("/", handleHttp)
	srv := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: nil}
	go func() {
		err := srv.ListenAndServe()
		logger.Debug("Web server stopped")
		if err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	abort := make(chan struct{})
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt)
	go func() {
		for sig := range sigs {
			logger.Infof("got OS signal % s", sig)
			if sig == os.Interrupt {
				logger.Info("Exiting...")
				srv.Shutdown(context.Background())
				close(abort)
				break
			}
		}
	}()

	bot, err := tgbotapi.NewBotAPI(cfg.TgToken)
	if err != nil {
		log.Panic(err)
	}
	bot.Debug = true
	log.Printf("Authorized on account % s", bot.Self.UserName)
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)
Loop:
	for {
		select {
		case update, ok := <-updates:
			if !ok {
				break Loop
			}
			if update.Message == nil { // ignore non-Message updates
				continue
			}
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, update.Message.Text)
			switch update.Message.Command() {
			case "open":
				msg.ReplyMarkup = numericKeyboard
			case "close":
				msg.ReplyMarkup = tgbotapi.NewRemoveKeyboard(true)
			}

			if _, err := bot.Send(msg); err != nil {
				log.Panic(err)
			}
		case <-abort:
			break Loop
		}
	}
}

const (
	paramNameSum     = "sum"
	paramNamePurpose = "purpose"
)

func handleHttp(w http.ResponseWriter, r *http.Request) {
	const qrPath = "/qr.jpg"
	if !(r.URL.Path == "/" || r.URL.Path == qrPath) {
		http.Error(w, "404 not found.", http.StatusNotFound)
		return
	}
	switch r.Method {
	case "GET":
		query := r.URL.Query()
		if r.URL.Path == qrPath {
			purpose := query.Get(paramNamePurpose)
			sum := query.Get(paramNameSum)
			if len(purpose) == 0 || len(sum) == 0 {
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
			imgBytes, err := qrcode.Encode(fmt.Sprintf("QRCodeTemplate", purpose, sum), qrcode.Medium, 256)
			if err != nil {
				logger.Errorf("error encoding qr code: %v", err)
				http.Error(w, fmt.Sprintf("500 error encoding qr code: %v", err), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "image/jpeg")
			io.Copy(w, bytes.NewReader(imgBytes))
			return
		}
		if len(query) == 0 {
			http.ServeFile(w, r, "form.html")
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
	case "POST":
		// Call ParseForm() to parse the raw query and update r.PostForm and r.Form.
		if err := r.ParseForm(); err != nil {
			logger.Errorf("ParseForm() err: %v", err)
			http.Error(w, fmt.Sprintf("500 error %v", err), http.StatusInternalServerError)
			return
		}
		params := url.Values{}
		params.Add(paramNameSum, r.FormValue(paramNameSum))
		params.Add(paramNamePurpose, r.FormValue(paramNamePurpose))
		http.Redirect(w, r, qrPath+"?"+params.Encode(), http.StatusFound)
	default:
		fmt.Fprint(w, "Sorry, only GET and POST methods are supported.")
	}
}

type Config struct {
	TgToken string
	QR      map[string]string
}
