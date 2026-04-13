package main

import (
	"7stgbot/gate"
	"7stgbot/tgsrv"
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh/terminal"

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
	debug   bool
	user    string
	pwd     string
)

func main() {
	flag.StringVar(&logfile, "logfile", "7stgbot.log", "log file")
	flag.StringVar(&cfgDir, "cfg", "./", "Path to config dir containing config.toml and data files")
	flag.BoolVar(&noTGBot, "notgbot", false, "Start telegram bot (must be configured in config)")
	flag.BoolVar(&debug, "debug", false, "set debug level of logger")
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
	tgsrv.AtomicLevel = zap.NewAtomicLevelAt(zap.InfoLevel)
	if debug {
		tgsrv.AtomicLevel.SetLevel(zap.DebugLevel)
	}
	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoderCfg),
		w,
		tgsrv.AtomicLevel,
	)
	logger = zap.New(core).Sugar()
	tgsrv.Logger = logger
	gate.Logger = logger
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
	g.ProxyUrl = cfg.ProxyUrl
	g.User = cfg.GateUser
	g.Password = cfg.GatePwd
	g.PalesPortalUser = cfg.PalesPortalUser
	g.PalesPortalPwd = cfg.PalesPortalPwd
	g.KeypadReleased = cfg.KeypadReleased
	g.Phones = make(map[string]*tgsrv.PalesUser)
	readCsv(filepath.Join(cfgDir, "pales_users.csv"), palgateUserFunc(g.Phones))
	g.RestrictedPhones = make(map[string]bool)
	readLines(filepath.Join(cfgDir, "gate-phones-restricted.txt"), func(s string, _ int) { g.RestrictedPhones[s] = true })
	g.PalesTokenFilename = filepath.Join(cfgDir, "t.txt")
	readLines(g.PalesTokenFilename, func(s string, i int) {
		if i == 0 {
			g.PalesPortalUserToken = s
		}
	})
	g.GateOpenNumber = cfg.GateOpenNumber
	g.GateInfoNumber = cfg.GateInfoNumber
	g.BLEPeriodSec = time.Duration(cfg.BLEPeriodSec)
	g.RateWatcher = &tgsrv.RateWatcher{
		Duration:         time.Duration(cfg.KeypadHitLimitDurationMinutes) * time.Minute,
		ThrottleDuration: time.Duration(cfg.KeypadThrottleMinutes) * time.Minute}
	g.RateWatcher.Init(cfg.KeypadHitLimit)
	g.CallStore = tgsrv.NewCallStore()
	g.PendingCalls = make(chan *gate.Call, 32)
	g.PendingSMSes = make(chan *gate.SMS, 32)
	g.SMSSession = make(map[int]*gate.SMS)
	g.SMSes = gate.NewSMSes()
	g.KeypadCodes = gate.NewKeypadCodes()
	g.Stored = make(chan struct{}, 8)
	g.TelegramNotification = make(chan *tgsrv.Notification, 32)
	g.NtfyNotification = make(chan *tgsrv.Notification, 32)
	g.KeypadCodesRequests = make(chan *tgsrv.PhoneSms, 32)

	fname := filepath.Join(cfgDir, "bt-macs.toml")
	if _, err := toml.DecodeFile(fname, &g.BTMacs); err != nil {
		logger.Errorf("error parsing  %q: %v", fname, err)
	}
	g.CfgDir = cfgDir
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
	Port                          int
	StaticDir                     string
	TgToken                       string
	Price                         map[string]float64
	Coef                          map[string]float64
	QR                            map[string]string
	DiscordAlertChannelURL        string
	IfTTTKey                      string
	AdminEmails                   []string
	AdminPhone                    string
	SMSRateLimiterCfg             map[string]int
	SMSRateLimiter                []tgsrv.Rate
	GateUrl                       string
	TelegramUrl                   string
	TelegramChatId                string
	TelegramTimeoutSec            int
	ProxyUrl                      string
	GateUser                      string
	GatePwd                       string
	PalesPortalUser               string
	PalesPortalPwd                string
	BleWatchLocation              int
	GateOpenNumber                string
	GateInfoNumber                string
	BLEPeriodSec                  int64
	KeypadHitLimit                int
	KeypadHitLimitDurationMinutes int64
	KeypadThrottleMinutes         int64
	KeypadReleased                bool
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

func readLines(filePath string, f func(string, int)) {
	file, err := os.Open(filePath)
	if err != nil {
		logger.Errorf("error opening %s : %v", filePath, err)
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	i := 0
	for scanner.Scan() {
		f(scanner.Text(), i)
		i++
	}
	if err := scanner.Err(); err != nil {
		logger.Errorf("error reading %s : %v", filePath, err)
	}
}

func readCsv(filePath string, f func([]string, map[string]int)) {
	file, err := os.Open(filePath)
	if err != nil {
		logger.Errorf("error opening %s : %v", filePath, err)
		return
	}
	defer file.Close()

	reader := csv.NewReader(file)
	header, err := reader.Read()
	if err != nil {
		logger.Errorf("error readig header: %v", err)
		return
	}
	cols := make(map[string]int)
	for i, name := range header {
		cols[name] = i
	}
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		f(record, cols)
	}
}

func palgateUserFunc(m map[string]*tgsrv.PalesUser) func([]string, map[string]int) {
	return func(row []string, cols map[string]int) {
		u := tgsrv.PalesUserFromCsv(row, cols)
		m[row[cols["Phone number"]]] = u
	}
}
