package main

import (
	"7stgbot/tgsrv"
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/BurntSushi/toml"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var logger *zap.SugaredLogger
var (
	cfgPath string
	logfile string
)

func main() {
	flag.StringVar(&logfile, "logfile", "7stgbot.log", "log file")
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
	tgsrv.Logger = logger
	defer logger.Sync()

	log.SetOutput(w)

	var cfg Config
	if _, err := toml.DecodeFile(cfgPath, &cfg); err != nil {
		logger.Errorf("error parsing toml config by path %q: %v", cfgPath, err)
		flag.PrintDefaults()
		return
	}
	srv := tgsrv.StartWebServer(cfg.Port, cfg.StaticDir, cfg.QR, cfg.Price, cfg.Coef)

	abort := make(chan struct{})
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt)
	go func() {
		for sig := range sigs {
			logger.Infof("got OS signal %s", sig)
			if sig == os.Interrupt {
				logger.Info("Exiting...")
				srv.Shutdown(context.Background())
				close(abort)
				break
			}
		}
	}()
	tgsrv.RunBot(cfg.TgToken, abort)
}

type Config struct {
	Port      int
	StaticDir string
	TgToken   string
	Price     string
	Coef      string
	QR        map[string]string
}
