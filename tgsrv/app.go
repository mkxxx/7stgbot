package tgsrv

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc64"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

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
	return time.Duration(now.Unix()-s.Updated) <= 10*time.Minute
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
)

const (
	sessionCookieName = "gate_session"
	appDomain         = "gate.7slavka.ru"
)

func (g *Gate) RegisterGateAppHTTP(mux *http.ServeMux, staticDir string) {
	mux.Handle("/gate/app/", http.StripPrefix("/gate/app", http.FileServer(http.Dir(staticDir))))

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
	//http.HandleFunc("/", serveIndex)
	mux.HandleFunc("POST /gate/app/sms/send", g.handleSmsSend)
	mux.HandleFunc("POST /gate/app/sms/verify", g.handleSmsVerify)

	// API Проверки состояния и выхода
	mux.HandleFunc("/gate/app/check-session", g.handleCheckSession)
	mux.HandleFunc("POST /gate/app/logout", g.handleLogout)

	// API WebAuthn (Passkeys)
	mux.HandleFunc("POST /gate/app/register/begin", g.handleRegisterBegin)
	mux.HandleFunc("POST /gate/app/register/finish", g.handleRegisterFinish)
	mux.HandleFunc("POST /gate/app/login/begin", g.handleLoginBegin)
	mux.HandleFunc("POST /gate/app/login/finish", g.handleLoginFinish)

	// Главное исполнительное действие
	mux.HandleFunc("POST /gate/app/open", g.handleGateOpen)
}

func (g *Gate) getPhoneFromSession(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return "", false
	}
	s := HTTPSession{Token: cookie.Value}
	if ok, _ := g.Entities.Load(&s); ok {
		return s.Phone, ok
	}
	return "", false
}

func (g *Gate) createSession(w http.ResponseWriter, phone string) {
	token := generateUUID()
	s := HTTPSession{Token: token, Phone: phone}
	err := g.Entities.Insert(&s)
	if err == nil {
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    token,
			Path:     "/",
			HttpOnly: true, // Защита от XSS атак
			Secure:   true, // Передача только по HTTPS
			SameSite: http.SameSiteStrictMode,
			Expires:  time.Now().Add(365 * 24 * time.Hour), // Сессия на год
		})
	}
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
	sms := CodeSMS{Phone: req.Phone}
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
	var req struct {
		Phone string `json:"phone"`
		Code  string `json:"code"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "Неверный код", http.StatusUnauthorized)
		return
	}
	sms := CodeSMS{Phone: req.Phone}
	ok, _ := g.Entities.Load(&sms)
	if !ok || req.Code != sms.Code {
		http.Error(w, "Неверный код", http.StatusUnauthorized)
		return
	}
	u := WebUser{Phone: req.Phone}
	if ok, _ := g.Entities.Load(&u); !ok {
		g.Entities.Insert(&u)
	}
	g.createSession(w, req.Phone)
	w.WriteHeader(http.StatusOK)
}

func (g *Gate) handleCheckSession(w http.ResponseWriter, r *http.Request) {
	phone, authorized := g.getPhoneFromSession(r)
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
	phone, authorized := g.getPhoneFromSession(r)
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
	cookie, _ := r.Cookie(sessionCookieName)
	mu.Lock()
	webauthnCtxDB["reg_"+cookie.Value] = sessionData
	mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(options)
}

func (g *Gate) handleRegisterFinish(w http.ResponseWriter, r *http.Request) {
	phone, authorized := g.getPhoneFromSession(r)
	cookie, err := r.Cookie(sessionCookieName)
	if !authorized || err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	mu.Lock()
	sessionData, ok := webauthnCtxDB["reg_"+cookie.Value]
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
	delete(webauthnCtxDB, "reg_"+cookie.Value)
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
	targetUser := WebUser{Phone: req.Phone}
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
	// Если все ок — создаем постоянную HttpOnly сессию
	g.createSession(w, targetUser.Phone)

	// Подчищаем временные контексты входа
	mu.Lock()
	delete(webauthnCtxDB, loginStateKey)
	mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success", "phone": targetUser.Phone})
}

func (g *Gate) handleGateOpen(w http.ResponseWriter, r *http.Request) {
	phone, authorized := g.getPhoneFromSession(r)
	if !authorized {
		http.Error(w, "Доступ запрещен. Авторизуйтесь.", http.StatusForbidden)
		return
	}
	// Логика физического открытия ворот
	Logger.Debugf(" [!] СИГНАЛ НА РЕЛЕ: Пользователь %s нажал кнопку ОТКРЫТЬ ВОРОТА в %v\n", phone, time.Now().Format("15:04:05"))

	w.WriteHeader(http.StatusOK)
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
