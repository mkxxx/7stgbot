package tgsrv

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc64"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

type Pair[K, V any] struct {
	Key   K
	Value V
}

type WebUser struct {
	Phone       string
	Credentials []webauthn.Credential
}

func (u *WebUser) Type() string { return "WebUser" }
func (u *WebUser) ID() string   { return u.Phone }
func (u *WebUser) MarshalData() (string, error) {
	data, err := json.Marshal(u.Credentials)
	return string(data), err
}
func (u *WebUser) UnmarshalData(data string) error {
	return json.Unmarshal([]byte(data), &u.Credentials)
}

type HTTPSession struct {
	Token string
	Phone string
}

func (s *HTTPSession) Type() string                    { return "HTTPSession" }
func (s *HTTPSession) ID() string                      { return s.Token }
func (s *HTTPSession) MarshalData() (string, error)    { return s.Phone, nil }
func (s *HTTPSession) UnmarshalData(data string) error { s.Phone = data; return nil }

type CodeSMS struct {
	Phone   string
	Code    string
	Updated int64
}

func (s *CodeSMS) Type() string                    { return "CodeSMS" }
func (s *CodeSMS) ID() string                      { return s.Phone }
func (s *CodeSMS) MarshalData() (string, error)    { return s.Code, nil }
func (s *CodeSMS) UnmarshalData(data string) error { s.Code = data; return nil }
func (s *CodeSMS) UpdatedRef() *int64              { return &s.Updated }
func (s *CodeSMS) Actual(now time.Time) bool {
	return time.Duration(now.Unix()-s.Updated)*time.Second <= 10*time.Minute
}

// Реализация интерфейса webauthn.User
func (u *WebUser) WebAuthnID() []byte                         { return []byte(u.Phone) }
func (u *WebUser) WebAuthnName() string                       { return u.Phone }
func (u *WebUser) WebAuthnDisplayName() string                { return u.Phone }
func (u *WebUser) WebAuthnIcon() string                       { return "" }
func (u *WebUser) WebAuthnCredentials() []webauthn.Credential { return u.Credentials }

var (
	webAuthnConfig *webauthn.WebAuthn
	webauthnCtxDB  = make(map[string]*webauthn.SessionData) // Сессии WebAuthn: token -> data
	mu             sync.Mutex
	notDigitRE     = regexp.MustCompile(`[^0-9]`)
)

const (
	sessionCookieName = "gate_session"
	appDomain         = "gate.7slavka.ru"
	msgKindCliCnt     = "cli_cnt"
	msgKindMsgPer     = "msg_per"
	msgKindGateOpened = "sys_event"
)

func (g *Gate) RegisterGateAppHTTP(mux *http.ServeMux, staticDir string) {
	mux.Handle("GET /gate/app/{$}", InitSession(http.StripPrefix("/gate/app", http.FileServer(http.Dir(staticDir)))))
	mux.Handle("GET /gate/app/", http.StripPrefix("/gate/app", http.FileServer(http.Dir(staticDir))))

	var err error
	webAuthnConfig, err = webauthn.New(&webauthn.Config{
		RPDisplayName: "Gate",
		RPID:          "7slavka.ru",
		RPOrigins: []string{
			"https://7slavka.ru",
			"https://" + appDomain,
		},
	})
	if err != nil {
		Logger.Fatalf("Ошибка инициализации WebAuthn: %v", err)
		return
	}
	mux.HandleFunc("POST /gate/app/sms/send", g.handleSmsSend)
	mux.HandleFunc("POST /gate/app/sms/verify", g.handleSmsVerify)

	// API Проверки состояния и выхода
	mux.HandleFunc("GET /gate/app/check-session", g.handleCheckSession)
	mux.HandleFunc("POST /gate/app/logout", g.handleLogout)

	// API WebAuthn (Passkeys)
	mux.HandleFunc("POST /gate/app/register/begin", g.handleRegisterBegin)
	mux.HandleFunc("POST /gate/app/register/finish", g.handleRegisterFinish)
	mux.HandleFunc("POST /gate/app/login/begin", g.handleLoginBegin)
	mux.HandleFunc("POST /gate/app/login/finish", g.handleLoginFinish)

	// Главное исполнительное действие
	mux.HandleFunc("POST /gate/app/open", g.handleGateOpen)

	go broker.run()

	mux.HandleFunc("POST /gate/app/chat/send", g.handleChatSend)
	mux.HandleFunc("GET /gate/app/chat/stream", g.handleChatStream)
}

func InitSession(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie(sessionCookieName); err == nil {
			h.ServeHTTP(w, r)
			return
		}
		token := generateUUID()
		cookie := &http.Cookie{
			Name:     sessionCookieName,
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteStrictMode,
			Expires:  time.Now().Add(365 * 24 * time.Hour),
		}
		http.SetCookie(w, cookie)
		r.AddCookie(cookie)

		h.ServeHTTP(w, r)
	})
}

func (g *Gate) getSessionInfo(r *http.Request) (token string, phone string, authorized bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return "", "", false
	}
	s := HTTPSession{Token: cookie.Value}
	ok, _ := g.Entities.Load(&s)
	return s.Token, s.Phone, ok
}

func (g *Gate) handleSmsSend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Phone string `json:"phone"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.Phone == "" {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	now := time.Now()
	sms := CodeSMS{Phone: normalizePhone(req.Phone)}
	exists, _ := g.Entities.Load(&sms)
	if !exists || !sms.Actual(now) {
		sms.Code = generateSMSCode()
		sms.Updated = now.Unix()
	}
	g.sendSMS(req.Phone, fmt.Sprintf("%s: введите код подтверждения %s", appDomain, sms.Code), time.Now().Add(time.Minute))
	w.WriteHeader(http.StatusOK)
	if exists {
		g.Entities.Update(&sms)
	} else {
		g.Entities.Insert(&sms)
	}
}

func generateSMSCode() string {
	bb := make([]byte, 4)
	rand.Read(bb)
	return strconv.Itoa((int(bb[0]) + int(bb[1])<<8 + int(bb[2])<<16 + int(bb[3])<<24) % 10000)
}

func (g *Gate) handleSmsVerify(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		http.Error(w, "Сессия не инициализирована. Перезагрузите страницу.", http.StatusBadRequest)
		return
	}
	var req struct {
		Phone string `json:"phone"`
		Code  string `json:"code"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "Неверный код", http.StatusUnauthorized)
		return
	}
	phone := normalizePhone(req.Phone)
	sms := CodeSMS{Phone: phone}
	ok, _ := g.Entities.Load(&sms)
	if !ok || req.Code != sms.Code {
		http.Error(w, "Неверный код", http.StatusUnauthorized)
		return
	}
	u := WebUser{Phone: phone}
	if ok, _ := g.Entities.Load(&u); !ok {
		g.Entities.Insert(&u)
	}
	s := HTTPSession{Token: cookie.Value, Phone: normalizePhone(phone)}
	g.Entities.Insert(&s)
	w.WriteHeader(http.StatusOK)
}

func (g *Gate) handleCheckSession(w http.ResponseWriter, r *http.Request) {
	_, phone, authorized := g.getSessionInfo(r)
	if !authorized {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	// Просто возвращаем номер телефона авторизованного пользователя
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"phone": phone,
	})
}

func (g *Gate) handleRegisterBegin(w http.ResponseWriter, r *http.Request) {
	token, phone, authorized := g.getSessionInfo(r)
	if !authorized {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	u := WebUser{Phone: phone}
	g.Entities.Load(&u)
	options, sessionData, err := webAuthnConfig.BeginRegistration(&u)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	mu.Lock()
	webauthnCtxDB["reg_"+token] = sessionData
	mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(options)
}

func (g *Gate) handleRegisterFinish(w http.ResponseWriter, r *http.Request) {
	token, phone, authorized := g.getSessionInfo(r)
	if !authorized {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	mu.Lock()
	sessionData, ok := webauthnCtxDB["reg_"+token]
	mu.Unlock()

	if !ok {
		http.Error(w, "WebAuthn Session Expired", http.StatusBadRequest)
		return
	}
	parsedCredential, err := protocol.ParseCredentialCreationResponse(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	u := WebUser{Phone: phone}
	exists, _ := g.Entities.Load(&u)
	credential, err := webAuthnConfig.CreateCredential(&u, *sessionData, parsedCredential)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	u.Credentials = append(u.Credentials, *credential)
	if exists {
		g.Entities.Update(&u)
	} else {
		g.Entities.Insert(&u)
	}
	mu.Lock()
	delete(webauthnCtxDB, "reg_"+token)
	mu.Unlock()

	w.WriteHeader(http.StatusOK)
}

func (g *Gate) handleLoginBegin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Phone string `json:"phone"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.Phone == "" {
		http.Error(w, "Некорректный запрос. Укажите номер телефона.", http.StatusBadRequest)
		return
	}
	targetUser := WebUser{Phone: normalizePhone(req.Phone)}
	exists, _ := g.Entities.Load(&targetUser)
	if !exists || len(targetUser.Credentials) == 0 {
		http.Error(w, "Пользователь не найден или не настроил Passkey", http.StatusNotFound)
		return
	}
	options, sessionData, err := webAuthnConfig.BeginLogin(&targetUser)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	loginStateKey := fmt.Sprintf("log_%d", time.Now().UnixNano())
	mu.Lock()
	webauthnCtxDB[loginStateKey] = sessionData
	mu.Unlock()

	w.Header().Set("X-Login-State", loginStateKey)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(options)
}

func (g *Gate) handleLoginFinish(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		http.Error(w, "Сессия не инициализирована. Перезагрузите страницу.", http.StatusBadRequest)
		return
	}
	loginStateKey := r.Header.Get("X-Login-State")

	mu.Lock()
	sessionData, ok := webauthnCtxDB[loginStateKey]
	mu.Unlock()

	if !ok {
		http.Error(w, "Временная сессия WebAuthn не найдена или истекла", http.StatusBadRequest)
		return
	}
	parsedCredential, err := protocol.ParseCredentialRequestResponse(r)
	if err != nil {
		http.Error(w, "Ошибка парсинга ответа биометрии: "+err.Error(), http.StatusBadRequest)
		return
	}
	targetUser := WebUser{Phone: string(sessionData.UserID)}
	exists, _ := g.Entities.Load(&targetUser)
	if !exists {
		http.Error(w, "Пользователь, привязавший этот ключ, больше не существует", http.StatusNotFound)
		return
	}
	// Криптографически проверяем подпись устройства на основе открытого ключа пользователя
	_, err = webAuthnConfig.ValidateLogin(&targetUser, *sessionData, parsedCredential)
	if err != nil {
		http.Error(w, "Криптографическая проверка подписи провалена", http.StatusUnauthorized)
		return
	}
	s := HTTPSession{Token: cookie.Value, Phone: targetUser.Phone}
	g.Entities.Insert(&s)

	// Подчищаем временные контексты входа
	mu.Lock()
	delete(webauthnCtxDB, loginStateKey)
	mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success", "phone": targetUser.Phone})
}

func (g *Gate) handleGateOpen(w http.ResponseWriter, r *http.Request) {
	_, phone, authorized := g.getSessionInfo(r)
	if !authorized {
		http.Error(w, "Доступ запрещен. Авторизуйтесь.", http.StatusForbidden)
		return
	}
	phone = normalizePhone(phone)[1:]
	u, ok := g.Phones[phone]
	if !ok {
		http.Error(w, "Вы не зарегестрированы в реестре шлагбаума. Обратитесь в правление.", http.StatusForbidden)
		return
	}
	if !g.allowedNow(phone) {
		http.Error(w, "В данный момент у вас не прав на проезд.", http.StatusForbidden)
		return
	}
	g.openGate(phone+" web app", "")
	g.sendSystemNotification(fmt.Sprintf("opened by web app %s %s", phone, u.name()))

	w.WriteHeader(http.StatusOK)
}

func normalizePhone(phone string) string {
	phone = notDigitRE.ReplaceAllString(phone, "")
	if strings.HasPrefix(phone, "8") {
		return "+7" + phone[1:]
	}
	return "+" + phone
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	return !strings.ContainsFunc(s, func(r rune) bool {
		return r < '0' || r > '9'
	})
}

func (g *Gate) handleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err == nil {
		s := HTTPSession{Token: cookie.Value}
		g.Entities.Delete(&s)
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/", Expires: time.Unix(0, 0), HttpOnly: true,
	})
	w.WriteHeader(http.StatusOK)
}

func generateUUID() string {
	uuid := make([]byte, 8)
	_, err := rand.Read(uuid)
	if err == nil {
		return fmt.Sprintf("%x", uuid)
	}
	table := crc64.MakeTable(crc64.ISO)
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(time.Now().UnixNano()))
	checksum := crc64.Checksum(buf, table)
	binary.BigEndian.PutUint64(buf, checksum)
	return fmt.Sprintf("%x", buf)
}

type Message struct {
	Phone       string          `json:"phone"`
	Text        string          `json:"text"`
	Time        time.Time       `json:"time"`
	Formatted   string          `json:"formatted_time"`
	IsMyMessage bool            `json:"is_my_message"`
	IsHistory   bool            `json:"is_history"`
	MsgKind     string          `json:"msg_kind"`
	authorized  bool            `json:"-"`
	target      map[string]bool `json:"-"`
}

func (m *Message) isHistorical() bool {
	return m.MsgKind != msgKindCliCnt
}

// Брокер чата с историей сообщений
type ChatBroker struct {
	clients        map[chan Message]string
	newClient      chan Pair[chan Message, string]
	defClient      chan chan Message
	messages       chan Message
	messageHistory []Message // Хранилище сообщений за последний час
}

var broker = &ChatBroker{
	clients:        make(map[chan Message]string),
	newClient:      make(chan Pair[chan Message, string]),
	defClient:      make(chan chan Message),
	messages:       make(chan Message),
	messageHistory: make([]Message, 0),
}

// Запуск брокера в отдельной горутине (вызвать в func main)
func (b *ChatBroker) run() {
	cleanupTicker := time.NewTicker(1 * time.Minute)
	guestNames := make(map[string]string)
	for {
		select {
		case p := <-b.newClient:
			token := p.Value
			b.clients[p.Key] = token
			if token != "" {
				guestID := time.Now().UnixMilli() % 10000
				guestNames[token] = fmt.Sprintf("Гость #%04d", guestID)
			}
			// При подключении нового клиента (или обновлении страницы)
			// отправляем ему всю сохраненную историю за последний час
			historyCopy := make([]Message, len(b.messageHistory))
			copy(historyCopy, b.messageHistory)
			go func(c chan Message) {
				for _, msg := range historyCopy {
					msg.IsHistory = true
					c <- msg
				}
			}(p.Key)
			b.sendClientsCounter()

		case ch := <-b.defClient:
			if token := b.clients[ch]; token != "" {
				delete(guestNames, token)
			}
			delete(b.clients, ch)
			close(ch)
			b.sendClientsCounter()

		case msg := <-b.messages:
			if !msg.authorized {
				name := guestNames[msg.Phone]
				if name == "" {
					name = "Неизвестный"
					mapStr := ""
					jsonData, err := json.Marshal(guestNames)
					if err != nil {
						mapStr = fmt.Sprintf("error: %v", err)
					} else {
						mapStr = string(jsonData)
					}
					Logger.Debugf("client name not found for %s in %s", msg.Phone, mapStr)
				}
				msg.Phone = name
			}
			if msg.isHistorical() {
				// Сохраняем сообщение в историю
				b.messageHistory = append(b.messageHistory, msg)
			}
			// Рассылаем всем активным клиентам
			b.fanoutMessage(msg)

		case <-cleanupTicker.C:
			// Удаляем сообщения старше 1 часа
			now := time.Now()
			validMessages := make([]Message, 0)
			for _, msg := range b.messageHistory {
				if now.Sub(msg.Time) < 1*time.Hour {
					validMessages = append(validMessages, msg)
				}
			}
			b.messageHistory = validMessages
		}
	}
}

func (b *ChatBroker) fanoutMessage(msg Message) {
	for clientChan := range b.clients {
		select {
		case clientChan <- msg:
		default:
			// Защита от зависших каналов
		}
	}
}

func (b *ChatBroker) sendClientsCounter() {
	msg := Message{MsgKind: msgKindCliCnt, Text: fmt.Sprintf("%d", len(b.clients))}
	b.fanoutMessage(msg)
}

func (g *Gate) handleChatSend(w http.ResponseWriter, r *http.Request) {
	token, phone, authorized := g.getSessionInfo(r)
	var req struct {
		Text string `json:"text"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.Text == "" {
		http.Error(w, "Пустое сообщение", http.StatusBadRequest)
		return
	}
	if !authorized {
		phone = token
	}
	now := time.Now()
	msg := Message{
		Phone:      phone,
		Text:       req.Text,
		Time:       now,
		Formatted:  now.Format("15:04"), // Форматируем время в ЧЧ:ММ по серверу
		authorized: authorized,
	}
	broker.messages <- msg
	w.WriteHeader(http.StatusOK)
	Logger.Debugf("web message from %s: %s", msg.Phone, msg.Text)
}

func (g *Gate) handleChatStream(w http.ResponseWriter, r *http.Request) {
	// Узнаем, какой телефон слушает этот конкретный поток (если авторизован)
	token, currentPhone, authorized := g.getSessionInfo(r)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.Header().Set("X-Accel-Buffering", "no")

	if !authorized {
		currentPhone = token
	}
	messageChan := make(chan Message)
	broker.newClient <- Pair[chan Message, string]{messageChan, currentPhone}

	defer func() {
		broker.defClient <- messageChan
	}()

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}
	fmt.Fprintf(w, ": ping\n\n")
	flusher.Flush()

	Logger.Debugf("event stream connected for: %s", currentPhone)

	pingTicker := time.NewTicker(15 * time.Second)
	defer pingTicker.Stop()

	for {
		select {
		case msg := <-messageChan:
			msg.IsMyMessage = msg.Phone == currentPhone // safe due too we got а copy from channel
			if msg.target[currentPhone] {
				msg.MsgKind = msgKindMsgPer
			}
			if len(msg.Phone) == 12 && digits(msg.Phone[1:]) {
				msg.Phone = msg.Phone[:3] + "*****" + msg.Phone[8:]
			}
			jsonBytes, err := json.Marshal(msg)
			if err != nil {
				continue
			}
			_, err = fmt.Fprintf(w, "data: %s\n\n", string(jsonBytes))
			if err != nil {
				return
			}
			flusher.Flush()

		case <-pingTicker.C:
			_, err := fmt.Fprintf(w, ": keepalive ping\n\n")
			if err != nil {
				return
			}
			flusher.Flush()

		case <-r.Context().Done():
			return
		}
	}
}
