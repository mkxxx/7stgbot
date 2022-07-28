package tgsrv

import (
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
	"strconv"
	"strings"
)

var Logger *zap.SugaredLogger

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

func RunBot(token string, abort chan struct{}, ws *webSrv, emailClient *EmailClient) {
	Logger.Infof("starting tg bot")
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		Logger.Error(err)
		<-abort
		return
	}
	bot.Debug = true
	Logger.Infof("authorized on account %s", bot.Self.UserName)
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
			Logger.Debugf("BOT: chatID=%d cmd=%q %q", update.Message.Chat.ID, update.Message.Command(),
				update.Message.Text)

			if update.Message.Location != nil {
				l := update.Message.Location
				Logger.Debugf("BOT: chatID=%d %.7f %.7f", update.Message.Chat.ID, l.Latitude, l.Longitude)
			}
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, update.Message.Text)
			switch update.Message.Command() {
			case "open":
				msg.ReplyMarkup = numericKeyboard
			case "close":
				msg.ReplyMarkup = tgbotapi.NewRemoveKeyboard(true)
			case "7s_electr":
				args := strings.Split(update.Message.Text, " ")
				i := 0
				var d, n string
				for _, arg := range args {
					if len(arg) == 0 {
						continue
					}
					if i == 1 {
						d = arg
					} else if i == 2 {
						n = arg
					}
					Logger.Debugf("%q", arg)
					i++
				}
				if i != 3 {
					msg := tgbotapi.NewMessage(update.Message.Chat.ID,
						"передайте дату в формате ггггмм и номер уч-ка")
					sendMessage(bot, msg)
					continue Loop
				}
				if len(d) < 6 {
					msg := tgbotapi.NewMessage(update.Message.Chat.ID,
						"передайте дату в формате ггггмм и номер уч-ка")
					sendMessage(bot, msg)
					continue Loop
				}
				d = d[:6]
				if d < "202201" || d > "2055012" {
					mtxt := fmt.Sprintf("%s не является датой", d)
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, mtxt)
					sendMessage(bot, msg)
					continue Loop
				}
				email := ws.registry.getEmail(d, n)
				if len(email) == 0 {
					mtxt := "email не найден. Сообщите email для внесения в реестр садоводов."
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, mtxt)
					sendMessage(bot, msg)
					continue Loop
				}
				y, err := strconv.Atoi(d[:4])
				if err != nil {
					mtxt := fmt.Sprintf("%s %v", d[:4], err)
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, mtxt)
					sendMessage(bot, msg)
					continue Loop
				}
				m, err := strconv.Atoi(d[4:6])
				if err != nil {
					mtxt := fmt.Sprintf("%s %v", d[4:6], err)
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, mtxt)
					sendMessage(bot, msg)
					continue Loop
				}
				if emailClient != nil {
					qrurl := QRURLInt(y, m, n)
					emailClient.sendEmail(email, "QR link", qrurl)
					Logger.Infof("sent email: %s %s", email, qrurl)
					mtxt := "ссылка на QR-код вам отправлена в письме"
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, mtxt)
					sendMessage(bot, msg)
				}
			}
		case <-abort:
			break Loop
		}
	}
}

func sendMessage(bot *tgbotapi.BotAPI, msg tgbotapi.MessageConfig) {
	if _, err := bot.Send(msg); err != nil {
		Logger.Errorf("error sending to chat %d %q %v", msg.ChatID, msg.Text, err)
	}
}
