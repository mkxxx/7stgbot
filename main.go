package main

import (
	"7stgbot/tgsrv"
	"bufio"
	"flag"
	"fmt"
	"golang.org/x/crypto/ssh/terminal"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var logger *zap.SugaredLogger
var (
	cfgDir  string
	logfile string
	noTGBot bool
	user    string
	pwd     string
)

func main() {
	flag.StringVar(&logfile, "logfile", "7stgbot.log", "log file")
	flag.StringVar(&cfgDir, "cfg", "./", "Path to config dir containing config.toml and data files")
	flag.BoolVar(&noTGBot, "notgbot", false, "Start telegram bot (must be configured in config)")
	flag.StringVar(&user, "u", "", "user   For development env only. Do not to be used in other environments")
	flag.StringVar(&pwd, "p", "", "user   For development env only. Do not to be used in other environments")
	flag.Parse()

	if (len(user) != 0) != (len(pwd) != 0) {
		log.Printf("user and pwd to be used both or none")
		flag.PrintDefaults()
		return
	}

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
	timeEncoder := zapcore.TimeEncoderOfLayout("2006-01-02_15:04:05.000")
	encoderCfg.EncodeTime = func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
		timeEncoder(t.In(time.Local), enc)
	}
	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoderCfg),
		w,
		zap.DebugLevel,
	)
	logger = zap.New(core).Sugar()
	tgsrv.Logger = logger
	defer logger.Sync()

	log.SetOutput(w)

	var emailClient *tgsrv.EmailClient
	var u, p string
	if len(user) != 0 {
		u, p = user, pwd
	} else if !noTGBot {
		u, p = stdinCredentials()
	}
	if len(u) != 0 && len(p) != 0 {
		emailClient = tgsrv.NewEmailClient(u, p, u)
	}

	abort := make(chan struct{})
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt)
	go func() {
		for sig := range sigs {
			logger.Infof("got OS signal %s", sig)
			if sig == os.Interrupt {
				logger.Info("Exiting...")
				close(abort)
				break
			}
		}
	}()

	var cfg Config
	cfgPath := filepath.Join(cfgDir, "config.toml")
	if _, err := toml.DecodeFile(cfgPath, &cfg); err != nil {
		logger.Errorf("error parsing toml config by path %q: %v", cfgPath, err)
		flag.PrintDefaults()
		return
	}
	for k, v := range cfg.SMSRateLimiterCfg {
		d, err := time.ParseDuration(k)
		if err != nil {
			logger.Errorf("error parsing config RateLimiterCfg, bad duration %s, %v", k, err)
			return
		}
		cfg.SMSRateLimiter = append(cfg.SMSRateLimiter, tgsrv.Rate{Ticker: d, Cnt: v})
	}
	sort.Sort(tgsrv.ByRate(cfg.SMSRateLimiter))

	pinger := tgsrv.StartPinger(abort, cfg.DiscordAlertChannelURL)

	var g tgsrv.Gate
	g.GateUrl = cfg.GateUrl
	g.TelegramUrl = cfg.TelegramUrl
	g.TelegramChatId = cfg.TelegramChatId
	g.TelegramTimeoutSec = cfg.TelegramTimeoutSec
	g.User = cfg.GateUser
	g.Password = cfg.GatePwd
	g.Phones = make(map[string]bool)
	readLines(filepath.Join(cfgDir, "gate-phones.txt"), func(s string) { g.Phones[s] = true })
	g.RestrictedPhones = make(map[string]bool)
	readLines(filepath.Join(cfgDir, "gate-phones-restricted.txt"), func(s string) { g.RestrictedPhones[s] = true })
	g.IgnoreBluetoothMacs = make(map[string]bool)
	readLines(filepath.Join(cfgDir, "macs-ignored.txt"), func(s string) { g.IgnoreBluetoothMacs[s[:min(17, len(s))]] = true })
	g.BluetoothMacNames = make(map[string]string)
	readLines(filepath.Join(cfgDir, "macs.txt"), func(s string) { g.BluetoothMacNames[s[:min(17, len(s))]] = s })
	g.Init()

	ws := tgsrv.StartWebServer(cfg.Port, cfg.StaticDir, cfgDir, cfg.QR, cfg.Price, cfg.Coef, abort, pinger, &g)

	if noTGBot {
		<-abort
	} else {
		err := tgsrv.RunBot(cfg.TgToken, abort, ws, emailClient, cfg.IfTTTKey, cfg.AdminPhone, cfg.AdminEmails,
			cfg.SMSRateLimiter)
		if err != nil {
			logger.Error(err)
		}
		<-abort
	}
}

type Config struct {
	Port                   int
	StaticDir              string
	TgToken                string
	Price                  map[string]float64
	Coef                   map[string]float64
	QR                     map[string]string
	DiscordAlertChannelURL string
	IfTTTKey               string
	AdminEmails            []string
	AdminPhone             string
	SMSRateLimiterCfg      map[string]int
	SMSRateLimiter         []tgsrv.Rate
	GateUrl                string
	TelegramUrl            string
	TelegramChatId         string
	TelegramTimeoutSec     int
	GateUser               string
	GatePwd                string
}

func stdinCredentials() (string, string) {
	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Enter Username: ")
	username, err := reader.ReadString('\n')
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print("Enter Password: ")
	bytePassword, err := terminal.ReadPassword(int(syscall.Stdin))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println()
	password := string(bytePassword)
	return strings.TrimSpace(username), strings.TrimSpace(password)
}

func readLines(filePath string, f func(string)) {
	file, err := os.Open(filePath)
	if err != nil {
		logger.Errorf("error opening %s : %v", filePath, err)
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		f(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		logger.Errorf("error reading %s : %v", filePath, err)
	}
}
