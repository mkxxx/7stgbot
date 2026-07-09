package gate

import (
	"encoding/json"
	"net/http"
	"sync"

	// Импортируем основной пакет и подпакет protocol для парсинга ответов
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// --- ИМИТАЦИЯ БАЗЫ ДАННЫХ ---
type User struct {
	id          []byte
	name        string
	displayName string
	credentials []webauthn.Credential
}

// Реализация интерфейса webauthn.User для версии v0.17+
func (u *User) WebAuthnID() []byte                         { return u.id }
func (u *User) WebAuthnName() string                       { return u.name }
func (u *User) WebAuthnDisplayName() string                { return u.displayName }
func (u *User) WebAuthnIcon() string                       { return "" }
func (u *User) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

var (
	webAuthnConfig *webauthn.WebAuthn
	// Тестовый пользователь
	testUser = &User{
		id:          []byte("super-secure-user-id-123"),
		name:        "user@example.com",
		displayName: "Иван Иванов",
	}
	// Хранилище активных сессий WebAuthn
	sessionStoreHost = make(map[string]*webauthn.SessionData)
	sessionMu        sync.Mutex
)

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
	http.HandleFunc("/gate/app/register/begin", registerBegin)
	http.HandleFunc("/gate/app/register/finish", registerFinish)
	http.HandleFunc("/gate/app/login/begin", loginBegin)
	http.HandleFunc("/gate/app/login/finish", loginFinish)

}

/*func serveIndex(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "index.html")
}*/

func registerBegin(w http.ResponseWriter, r *http.Request) {
	options, sessionData, err := webAuthnConfig.BeginRegistration(testUser)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sessionMu.Lock()
	sessionStoreHost["reg_session"] = sessionData
	sessionMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(options)
}

func registerFinish(w http.ResponseWriter, r *http.Request) {
	sessionMu.Lock()
	sessionData, exists := sessionStoreHost["reg_session"]
	sessionMu.Unlock()
	if !exists {
		http.Error(w, "Сессия не найдена", http.StatusBadRequest)
		return
	}
	parsedCredential, err := protocol.ParseCredentialCreationResponse(r)
	if err != nil {
		http.Error(w, "Ошибка парсинга ответа: "+err.Error(), http.StatusBadRequest)
		return
	}
	// Валидируем данные на основе сохраненной сессии
	credential, err := webAuthnConfig.CreateCredential(testUser, *sessionData, parsedCredential)
	if err != nil {
		http.Error(w, "Ошибка валидации ключа: "+err.Error(), http.StatusBadRequest)
		return
	}
	// Сохраняем созданный публичный ключ в профиль пользователя
	testUser.credentials = append(testUser.credentials, *credential)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success"}`))
}

func loginBegin(w http.ResponseWriter, r *http.Request) {
	options, sessionData, err := webAuthnConfig.BeginLogin(testUser)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sessionMu.Lock()
	sessionStoreHost["login_session"] = sessionData
	sessionMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(options)
}

func loginFinish(w http.ResponseWriter, r *http.Request) {
	sessionMu.Lock()
	sessionData, exists := sessionStoreHost["login_session"]
	sessionMu.Unlock()
	if !exists {
		http.Error(w, "Сессия не найдена", http.StatusBadRequest)
		return
	}
	parsedCredential, err := protocol.ParseCredentialRequestResponse(r)
	if err != nil {
		http.Error(w, "Ошибка парсинга подписи: "+err.Error(), http.StatusBadRequest)
		return
	}
	// Проверяем подпись с помощью ранее сохраненного публичного ключа пользователя
	_, err = webAuthnConfig.ValidateLogin(testUser, *sessionData, parsedCredential)
	if err != nil {
		http.Error(w, "Криптографическая проверка провалена: "+err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success"}`))
}
