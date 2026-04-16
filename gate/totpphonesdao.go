package gate

import (
	"database/sql"
)

const createTOTP string = `
  CREATE TABLE IF NOT EXISTS totp (
  id INTEGER PRIMARY KEY,
  phone TEXT NOT NULL,
  created_at_ms int NOT NULL
  );`

const totpFile string = "totp.db"

type TOTPPhones struct {
	db *sql.DB
}

type TOTPPhone struct {
	ID             int
	Phone          string
	CreatedAtMilli int64
}

type TOTPPhonesDAO interface {
	ListEndsWith(string) ([]TOTPPhone, error)
	Insert(p *TOTPPhone) error
}

func NewTOTPPhones() TOTPPhonesDAO {
	db, err := sql.Open("sqlite3", smsFile)
	if err != nil {
		Logger.Errorf("opening %s %v", smsFile, err)
		return &NullTOTPPhones{}
	}
	if _, err := db.Exec(createSMSes); err != nil {
		Logger.Errorf("creating table %s %v", smsFile, err)
		return &NullTOTPPhones{}
	}
	return &TOTPPhones{
		db: db,
	}
}

func (s *TOTPPhones) Insert(p *TOTPPhone) error {
	_, err := s.db.Exec("INSERT INTO totp (phone, created_at_ms) VALUES(?,?);",
		p.Phone, p.CreatedAtMilli)
	if err != nil {
		return err
	}
	return nil
}

func (s *TOTPPhones) ListEndsWith(postfix string) ([]TOTPPhone, error) {
	postfix = "%" + postfix
	rows, err := s.db.Query("SELECT id, phone, created_at_ms FROM totp WHERE phone LIKE ?", postfix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	phones := []TOTPPhone{}
	for rows.Next() {
		phone := TOTPPhone{}
		err = rows.Scan(&phone.ID, &phone.Phone, &phone.CreatedAtMilli)
		if err != nil {
			return nil, err
		}
		phones = append(phones, phone)
	}
	return phones, nil
}

type NullTOTPPhones struct {
}

func (s *NullTOTPPhones) ListEndsWith(postfix string) ([]TOTPPhone, error) {
	return nil, nil
}

func (s *NullTOTPPhones) Insert(p *TOTPPhone) error {
	return nil
}

func (s *NullTOTPPhones) Update(p *TOTPPhone) error {
	return nil
}
