package tgsrv

import (
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
	"strconv"
	"strings"
	"time"
)

const (
	botName                        = "snt7s_bot"
	tgBotCommandSmsAllWithoutEmail = "/7s_sms_all_without_email"
	tgBotCommandAllSendElectr      = "/7s_all_send_electr"
	tgBotCommandSearch             = "/7s_search"
	tgBotCommandQR                 = "qr"
	tgBotCommandSMS                = "/sms"
	tgBotCommandSMSAll             = "/sms_all"
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

type TGBot struct {
	bot            *tgbotapi.BotAPI
	abort          chan struct{}
	ws             *webSrv
	emailClient    *EmailClient
	users          *Users
	smses          *SMSes
	smsClient      *SMSClient
	SMSRateLimiter []Rate
	adminEmails    []string
	adminPhone     string
}

type Rate struct {
	Timer time.Duration
	Cnt   int
}

type ByRate []Rate

func (r ByRate) Len() int      { return len(r) }
func (r ByRate) Swap(i, j int) { r[i], r[j] = r[j], r[i] }
func (r ByRate) Less(i, j int) bool {
	return r[i].rateNano() < r[j].rateNano()
}

func (r *Rate) rateNano() time.Duration {
	return time.Duration(int(r.Timer.Nanoseconds()) / r.Cnt)
}

func RunBot(token string, abort chan struct{}, ws *webSrv, emailClient *EmailClient, iftttKey string,
	adminPhone string, adminEmails []string, SMSRateLimiter []Rate) error {

	Logger.Infof("starting tg bot")
	b := TGBot{abort: abort, ws: ws, emailClient: emailClient, smsClient: NewSMSClient(iftttKey),
		SMSRateLimiter: SMSRateLimiter, adminPhone: adminPhone, adminEmails: adminEmails}
	var err error
	b.users, err = NewUsers()
	if err != nil {
		return err
	}
	b.smses, err = NewSMSes()
	if err != nil {
		return err
	}
	b.bot, err = tgbotapi.NewBotAPI(token)
	if err != nil {
		return err
	}
	b.bot.Debug = true
	Logger.Infof("authorized on account %s", b.bot.Self.UserName)

	go b.smsSender()

	startedMsg := fmt.Sprintf("snt7s_bot is started at %s",
		time.Now().In(Location).Format("2006-01-02 15:04:05"))
	if b.emailClient != nil && len(adminEmails) != 0 {
		b.emailClient.sendEmail(adminEmails[0], startedMsg, startedMsg)
	}
	b.sms(adminPhone, startedMsg)

	b.run()
	return nil
}

func (b *TGBot) run() {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := b.bot.GetUpdatesChan(u)
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

			command := update.Message.Command()
			switch command {
			case "7s_electr":
				b.handleElectr(update)
			case "open":
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, update.Message.Text)
				msg.ReplyMarkup = numericKeyboard
				b.sendMessage(msg)
			case "close":
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, update.Message.Text)
				msg.ReplyMarkup = tgbotapi.NewRemoveKeyboard(true)
				b.sendMessage(msg)
			case "start":
				b.handleStart(update)
			case tgBotCommandQR:
				b.handleQR(update)
			case tgBotCommandSMS:
				b.handleSMS(update)
			default:
				text := update.Message.Text
				switch {
				case text == tgBotCommandAllSendElectr ||
					strings.HasPrefix(text, tgBotCommandAllSendElectr+"@"):

					b.handleSendElectrToAllTGSub(update)
				case strings.HasPrefix(text, tgBotCommandSmsAllWithoutEmail+" ") ||
					strings.HasPrefix(text, tgBotCommandSmsAllWithoutEmail+"@"+botName+" "):

					cmd := tgBotCommandSmsAllWithoutEmail
					if strings.HasPrefix(text, cmd+" ") {
						text = text[len(cmd)+1:]
					} else {
						text = text[len(cmd)+1+len(botName)+1:]
					}
					b.smsAllWithoutEmail(update, text)
				case strings.HasPrefix(text, tgBotCommandSearch+" ") ||
					strings.HasPrefix(text, tgBotCommandSearch+"@"+botName+" "):

					cmd := tgBotCommandSearch
					if strings.HasPrefix(text, cmd+" ") {
						text = text[len(cmd)+1:]
					} else {
						text = text[len(cmd)+1+len(botName)+1:]
					}
					b.search(update, text)
				case text == tgBotCommandSMSAll ||
					strings.HasPrefix(text, tgBotCommandSMSAll+"@"):

					cmd := tgBotCommandSMSAll
					if strings.HasPrefix(text, cmd+" ") {
						text = text[len(cmd)+1:]
					} else {
						text = text[len(cmd)+1+len(botName)+1:]
					}
					b.handleSMSAll(update, text)
				case text == tgBotCommandAllSendElectr ||
					strings.HasPrefix(text, tgBotCommandAllSendElectr+"@"):

					b.handleSendElectrToAllTGSub(update)
				}
			}
		case <-b.abort:
			break Loop
		}
	}
}

func (b *TGBot) handleElectr(update tgbotapi.Update) {
	args := strings.Fields(update.Message.Text)
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
		b.sendMessage(msg)
		return
	}
	if len(d) < 6 {
		msg := tgbotapi.NewMessage(update.Message.Chat.ID,
			"передайте дату в формате ггггмм и номер уч-ка")
		b.sendMessage(msg)
		return
	}
	d = d[:6]
	if d < "202201" || d > "2055012" {
		mtxt := fmt.Sprintf("%s не является датой", d)
		msg := tgbotapi.NewMessage(update.Message.Chat.ID, mtxt)
		b.sendMessage(msg)
		return
	}
	email := b.ws.registry.Load().(*Registry).getEmailByPlotNumber(n)
	if len(email) == 0 {
		mtxt := "email не найден. Сообщите email для внесения в реестр садоводов."
		msg := tgbotapi.NewMessage(update.Message.Chat.ID, mtxt)
		b.sendMessage(msg)
		return
	}
	y, err := strconv.Atoi(d[:4])
	if err != nil {
		mtxt := fmt.Sprintf("%s %v", d[:4], err)
		msg := tgbotapi.NewMessage(update.Message.Chat.ID, mtxt)
		b.sendMessage(msg)
		return
	}
	m, err := strconv.Atoi(d[4:6])
	if err != nil {
		mtxt := fmt.Sprintf("%s %v", d[4:6], err)
		msg := tgbotapi.NewMessage(update.Message.Chat.ID, mtxt)
		b.sendMessage(msg)
		return
	}
	if b.emailClient != nil {
		qrurl := QRURLInt(y, m, n)
		b.emailClient.sendEmail(email, "QR link", qrurl)
		Logger.Infof("sent email: %s %s", email, qrurl)
		mtxt := "ссылка на QR-код вам отправлена в письме"
		msg := tgbotapi.NewMessage(update.Message.Chat.ID, mtxt)
		b.sendMessage(msg)
	}
}

// https://t.me/snt7s_bot?start=xxxxxxxx
func (b *TGBot) handleStart(u tgbotapi.Update) {
	Logger.Debugf("/START chatID: %d %q", u.Message.Chat.ID, u.Message.Text)
	args := strings.Fields(u.Message.Text)
	if len(args) < 2 {
		return
	}
	key := args[1]
	email, err, ok := decodeEmailAndMD5(key)
	if err != nil {
		Logger.Errorf("error decoding %q %v", args[1], err)
		return
	}
	if !ok {
		return
	}
	user := User{
		Email:     email,
		ChatID:    u.Message.Chat.ID,
		CreatedAt: time.Now().Unix()}

	err = b.users.Insert(user)
	if err != nil {
		Logger.Errorf("error inserting user %s %d %v", user.Email, user.ChatID, err)
	}
	msg := tgbotapi.NewMessage(u.Message.Chat.ID, "Вы успешно подписаны!\n/qr - QR-код для оплаты электричества за предыдущий месяц")
	b.sendMessage(msg)
}

func (b *TGBot) sendMessage(msg tgbotapi.MessageConfig) {
	if _, err := b.bot.Send(msg); err != nil {
		Logger.Errorf("error sending to chat %d %q %v", msg.ChatID, msg.Text, err)
	}
}

func (b *TGBot) handleSendElectrToAllTGSub(u tgbotapi.Update) {
	if !b.authorizedActor(u.Message.Chat.ID, tgBotCommandAllSendElectr) {
		return
	}
	users, err := b.users.List()
	if err != nil {
		Logger.Errorf("users.List(): %v", err)
	}
	y, m, _ := time.Now().In(Location).AddDate(0, -1, 0).Date()
	mtxt := ([]string{"янв", "фев", "мар", "апр", "май", "июн", "июл", "авг", "мен", "окт", "ноя", "дек"})[m-1]
	for _, user := range users {
		//Logger.Infof("%s %d", u.Email, u.ChatID)
		plotNumbers := b.ws.FindByEmailPrefix(user.Email)
		for pn := range plotNumbers {
			url := QRURL(fmt.Sprintf("%d", y), fmt.Sprintf("%02d", int(m)), pn)
			msg := tgbotapi.NewMessage(user.ChatID,
				fmt.Sprintf("Ссылка на QR-кол для оплаты эл-ва за %s %d\n%s", mtxt, y, url))
			Logger.Debugf("SEND: email: %s chatID: %d %q", user.Email, user.ChatID, url)
			b.sendMessage(msg)
		}
	}
}

func (b *TGBot) smsAllWithoutEmail(u tgbotapi.Update, text string) {
	if !b.authorizedActor(u.Message.Chat.ID, tgBotCommandSmsAllWithoutEmail) {
		return
	}
	n := b.ws.registry.Load().(*Registry).SearchExec(func(r *SearchRecord) bool {
		b.sendSMSIfNoEmail(r.Email, r.Phone, text)
		return true
	})
	if n > 0 {
		return
	}
	b.ws.registry.Load().(*Registry).RegistryExec(func(r *RegistryRecord) bool {
		b.sendSMSIfNoEmail(r.Email, r.Phone, text)
		return true
	})
}

func (b *TGBot) sendSMSIfNoEmail(email string, phone string, text string) {
	if len(email) != 0 || len(phone) == 0 || len(text) == 0 {
		return
	}
	b.sms(phone, text)
}

func (b *TGBot) search(u tgbotapi.Update, text string) {
	if !b.authorizedActor(u.Message.Chat.ID, tgBotCommandSearch) {
		return
	}
	var rr []string
	n := b.ws.registry.Load().(*Registry).SearchExec(func(r *SearchRecord) bool {
		if strings.Contains(r.Email, text) ||
			strings.Contains(r.Name, text) ||
			strings.Contains(r.Phone, text) ||
			r.Login == text ||
			r.PlotNumber == text {

			rr = append(rr, fmt.Sprintf("%s %s %s %s %s",
				r.Login, r.Name, r.Email, r.Phone, r.PlotNumber))
		}
		return true
	})
	_ = n
	if len(rr) == 0 {
		b.ws.registry.Load().(*Registry).RegistryExec(func(r *RegistryRecord) bool {
			if strings.Contains(r.Email, text) ||
				strings.Contains(r.FIO, text) ||
				strings.Contains(r.Phone, text) ||
				r.Login == text ||
				r.PlotNumber == text {

				rr = append(rr, fmt.Sprintf("%s %s %s %s %s",
					r.Login, r.FIO, r.Email, r.Phone, r.PlotNumber))
			}
			return true
		})
	}
	more := len(rr) - 10
	if more > 0 {
		rr = rr[:10]
	}
	mtxt := strings.Join(rr, "\n")
	if more > 0 {
		mtxt += fmt.Sprintf("\n"+"  and %d more ...", more)
	}
	msg := tgbotapi.NewMessage(u.Message.Chat.ID, mtxt)
	b.sendMessage(msg)
}

func (b *TGBot) authorizedActor(chatID int64, cmd string) bool {
	actor := b.users.user(chatID)
	if actor == nil {
		Logger.Warnw("ACCESS DENIED: %s chatID %d", cmd, chatID)
		msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("У вас нет прав"))
		b.sendMessage(msg)
		return false
	}
	for _, email := range b.adminEmails {
		if email == actor.Email {
			return true
		}
	}
	return false
}

func (b *TGBot) handleQR(u tgbotapi.Update) {
	users, err := b.users.List()
	if err != nil {
		Logger.Errorf("users.List(): %v", err)
	}
	chatID := u.Message.Chat.ID
	y, m, _ := time.Now().In(Location).AddDate(0, -1, 0).Date()
	for _, user := range users {
		if user.ChatID == chatID {
			plotNumbers := b.ws.FindByEmailPrefix(user.Email)
			var urls []string
			for pn := range plotNumbers {
				urls = append(urls, QRURL(fmt.Sprintf("%d", y), fmt.Sprintf("%02d", int(m)), pn))
			}
			msg := tgbotapi.NewMessage(chatID, strings.Join(urls, "\n"))
			Logger.Debugf("QR: %s chatID: %d %s", user.Email, chatID, strings.Join(urls, " "))
			b.sendMessage(msg)
			return
		}
	}
}

func (b *TGBot) handleSMS(u tgbotapi.Update) {
	text := u.Message.Text
	i := strings.Index(text, " ")
	text = strings.TrimSpace(text[i+1:])
	i = strings.Index(text, " ")
	if i < 0 {
		return
	}
	phone := text[:i]
	text = strings.TrimSpace(text[i+1:])
	if len(text) == 0 {
		return
	}
	phone = strings.ReplaceAll(phone, " ", "")
	phone = strings.ReplaceAll(phone, "-", "")
	phone = strings.ReplaceAll(phone, "(", "")
	phone = strings.ReplaceAll(phone, ")", "")
	if !phoneRE.MatchString(phone) {
		return
	}
	b.sms(phone, text)
}

func (b *TGBot) handleSMSAll(u tgbotapi.Update, text string) {

}
