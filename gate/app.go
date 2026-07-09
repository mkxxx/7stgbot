package gate

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

type User struct {
	Phone       string
	Credentials []webauthn.Credential
}

// Реализация интерфейса webauthn.User
func (u *User) WebAuthnID() []byte                         { return []byte(u.Phone) }
func (u *User) WebAuthnName() string                       { return u.Phone }
func (u *User) WebAuthnDisplayName() string                { return u.Phone }
func (u *User) WebAuthnIcon() string                       { return "" }
func (u *User) WebAuthnCredentials() []webauthn.Credential { return u.Credentials }

var (
	webAuthnConfig *webauthn.WebAuthn
	usersDB        = make(map[string]*User)             // Хранилище пользователей: phone -> User
	sessionsDB     = make(map[string]string)            // Активные сессии: token -> phone
	webauthnCtxDB  = make(map[string]*webauthn.SessionData) // Сессии WebAuthn: token -> data
	mu             sync.Mutex
)

const sessionCookieName = "gate_session"

func RegisterGateAppHTTP(mux *http.ServeMux, staticDir string) {
	mux.Handle("/gate/app", http.StripPrefix("/gate/app", http.FileServer(http.Dir(staticDir))))

	var err error
	webAuthnConfig, err = webauthn.New(&webauthn.Config{
		RPDisplayName: "Gate",
		RPID:          "7slavka.ru",
		RPOrigins: []string{
			"https://7slavka.ru",
			"https://gate.7slavka.ru",
		},
	})
	if err != nil {
		Logger.Fatalf("Ошибка инициализации WebAuthn: %v", err)
		return
	}
	//http.HandleFunc("/", serveIndex)
	http.HandleFunc("/gate/app/sms/send", handleSmsSend)
	http.HandleFunc("/gate/app/sms/verify", handleSmsVerify)
	
	// API Проверки состояния и выхода
	http.HandleFunc("/gate/app/check-session", handleCheckSession)
	http.HandleFunc("/gate/app/logout", handleLogout)
	
	// API WebAuthn (Passkeys)
	http.HandleFunc("/gate/app/register/begin", handleRegisterBegin)
	http.HandleFunc("/gate/app/register/finish", handleRegisterFinish)
	http.HandleFunc("/gate/app/login/begin", handleLoginBegin)
	http.HandleFunc("/gate/app/login/finish", handleLoginFinish)
	
	// Главное исполнительное действие
	http.HandleFunc("/gate/app/gate/open", handleGateOpen)

}

func getPhoneFromSession(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return "", false
	}
	mu.Lock()
	phone, exists := sessionsDB[cookie.Value]
	mu.Unlock()
	return phone, exists
}

func createSession(w http.ResponseWriter, phone string) {
	token := fmt.Sprintf("token_%d", time.Now().UnixNano()) // В реальном коде используйте crypto/rand или UUID
	mu.Lock()
	sessionsDB[token] = phone
	mu.Unlock()

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

func handleSmsSend(w http.ResponseWriter, r *http.Request) {
	var req struct { Phone string `json:"phone"` }
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.Phone == "" {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	// Имитация отправки СМС. Код подтверждения жестко задан как "1234"
	Logger.Infof("[SMS-ШЛЮЗ] Отправлен код 1234 на номер %s\n", req.Phone)
	w.WriteHeader(http.StatusOK)
}

func handleSmsVerify(w http.ResponseWriter, r *http.Request) {
	var req struct { Phone string `json:"phone"`; Code  string `json:"code"` }
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.Code != "1234" {
		http.Error(w, "Неверный код", http.StatusUnauthorized)
		return
	}
	mu.Lock()
	if _, exists := usersDB[req.Phone]; !exists {
		usersDB[req.Phone] = &User{Phone: req.Phone}
	}
	mu.Unlock()

	createSession(w, req.Phone)
	w.WriteHeader(http.StatusOK)
}

func handleCheckSession(w http.ResponseWriter, r *http.Request) {
	phone, authorized := getPhoneFromSession(r)
	if !authorized {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	mu.Lock()
	user := usersDB[phone]
	mu.Unlock()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"phone":        phone,
		"has_webauthn": len(user.Credentials) > 0,
	})
}

func handleRegisterBegin(w http.ResponseWriter, r *http.Request) {
	phone, authorized := getPhoneFromSession(r)
	if !authorized {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	mu.Lock()
	user := usersDB[phone]
	mu.Unlock()

	options, sessionData, err := webAuthnConfig.BeginRegistration(user)
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

func handleRegisterFinish(w http.ResponseWriter, r *http.Request) {
	phone, authorized := getPhoneFromSession(r)
	cookie, err := r.Cookie(sessionCookieName)
	if !authorized || err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	mu.Lock()
	sessionData, exists := webauthnCtxDB["reg_"+cookie.Value]
	user := usersDB[phone]
	mu.Unlock()

	if !exists {
		http.Error(w, "WebAuthn Session Expired", http.StatusBadRequest)
		return
	}

	parsedCredential, err := protocol.ParseCredentialCreationResponse(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	credential, err := webAuthnConfig.CreateCredential(user, *sessionData, parsedCredential)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	mu.Lock()
	user.Credentials = append(user.Credentials, *credential)
	delete(webauthnCtxDB, "reg_"+cookie.Value)
	mu.Unlock()

	w.WriteHeader(http.StatusOK)
}

func handleLoginBegin(w http.ResponseWriter, r *http.Request) {
	// При входе по WebAuthn (Passkeys) мы можем использовать беспарольный поиск (Usernameless) 
	// Для простоты реализации привяжем к "последнему активному" или предложим серверный вызов.
	// Здесь берется первый попавшийся зарегистрированный пользователь для демо-целей.
	mu.Lock()
	var targetUser *User
	for _, u := range usersDB {
		if len(u.Credentials) > 0 {
			targetUser = u
			break
		}
	}
	mu.Unlock()

	if targetUser == nil {
		http.Error(w, "Нет зарегистрированных ключей в системе", http.StatusNotFound)
		return
	}

	options, sessionData, err := webAuthnConfig.BeginLogin(targetUser)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Создаем временный токен сессии входа
	loginStateKey := fmt.Sprintf("log_%d", time.Now().UnixNano())
	mu.Lock()
	webauthnCtxDB[loginStateKey] = sessionData
	mu.Unlock()

	// Передаем временный ключ отслеживания в заголовок ответа
	w.Header().Set("X-Login-State", loginStateKey)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(options)
}

func handleLoginFinish(w http.ResponseWriter, r *http.Request) {
	loginStateKey := r.Header.Get("X-Login-State")
	
	mu.Lock()
	sessionData, exists := webauthnCtxDB[loginStateKey]
	mu.Unlock()

	if !exists {
		http.Error(w, "Ключ сессии недействителен", http.StatusBadRequest)
		return
	}

	parsedCredential, err := protocol.ParseCredentialRequestResponse(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Ищем пользователя, которому принадлежит этот публичный ключ
	var targetUser *User
	mu.Lock()
	for _, u := range usersDB {
		if string(u.WebAuthnID()) == string(sessionData.UserID) {
			targetUser = u
			break
		}
	}
	mu.Unlock()

	_, err = webAuthnConfig.ValidateLogin(targetUser, *sessionData, parsedCredential)
	if err != nil {
		http.Error(w, "Ошибка подписи биометрии", http.StatusUnauthorized)
		return
	}

	// Авторизация успешна! Выдаем пользователю постоянную сессионную куку
	createSession(w, targetUser.Phone)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success", "phone": targetUser.Phone})
}

func handleGateOpen(w http.ResponseWriter, r *http.Request) {
	phone, authorized := getPhoneFromSession(r)
	if !authorized {
		http.Error(w, "Доступ запрещен. Авторизуйтесь.", http.StatusForbidden)
		return
	}

	// Логика физического открытия ворот
	Logger.Debugf(" [!] СИГНАЛ НА РЕЛЕ: Пользователь %s нажал кнопку ОТКРЫТЬ ВОРОТА в %v\n", phone, time.Now().Format("15:04:05"))
	
	w.WriteHeader(http.StatusOK)
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err == nil {
		mu.Lock()
		delete(sessionsDB, cookie.Value)
		mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/", Expires: time.Unix(0, 0), HttpOnly: true,
	})
	w.WriteHeader(http.StatusOK)
}
