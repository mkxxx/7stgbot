module 7stgbot

go 1.25.0

require (
	github.com/BurntSushi/toml v1.6.0
	// не обновлялась с 2021, Переходите на github.com/go-telegram/bot
	github.com/go-telegram-bot-api/telegram-bot-api/v5 v5.5.1
	github.com/gocarina/gocsv v0.0.0-20240520201108-78e41c74b4b1
	// github.com/ncruces/go-sqlite3  избавиться от CGO, многие сейчас переходят на
	github.com/mattn/go-sqlite3 v1.14.38
	github.com/robfig/cron/v3 v3.0.1
	github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e
	go.uber.org/zap v1.27.1
	golang.org/x/crypto v0.49.0
	// https://github.com/wneessen/go-mail
	gopkg.in/gomail.v2 v2.0.0-20160411212932-81ebce5c23df
	gopkg.in/natefinch/lumberjack.v2 v2.2.1
)

require (
	github.com/richardlehane/mscfb v1.0.6 // indirect
	github.com/richardlehane/msoleps v1.0.6 // indirect
	github.com/tiendc/go-deepcopy v1.7.2 // indirect
	github.com/xuri/efp v0.0.1 // indirect
	github.com/xuri/excelize/v2 v2.10.1 // indirect
	github.com/xuri/nfp v0.0.2-0.20250530014748-2ddeb826f9a9 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/term v0.41.0 // indirect
	golang.org/x/text v0.35.0 // indirect
	gopkg.in/alexcesaro/quotedprintable.v3 v3.0.0-20150716171945-2caba252f4dc // indirect
)
