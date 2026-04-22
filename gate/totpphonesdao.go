package gate

import (
	"database/sql"
)

const createTOTP string = `
  CREATE TABLE IF NOT EXISTS totp (
  phone TEXT PRIMARY KEY,
  created_at_ms int NOT NULL
  );`

type TOTPPhones struct {
	db *sql.DB
}

type TOTPPhone struct {
	Phone          string
	CreatedAtMilli int64
}

type TOTPPhonesDAO interface {
	ListEndsWith(string) ([]TOTPPhone, error)
	Insert(p *TOTPPhone) error
}

func NewTOTPPhones(db *sql.DB) TOTPPhonesDAO {
	if db == nil {
		return &NullTOTPPhones{}
	}
	if _, err := db.Exec(createTOTP); err != nil {
		Logger.Errorf("creating table totp %v", err)
		return &NullTOTPPhones{}
	}
	return &TOTPPhones{
		db: db,
	}
}

func (s *TOTPPhones) Insert(p *TOTPPhone) error {
	_, err := s.db.Exec("INSERT OR IGNORE INTO totp (phone, created_at_ms) VALUES(?,?);",
		p.Phone, p.CreatedAtMilli)
	if err != nil {
		return err
	}
	return nil
}

func (s *TOTPPhones) ListEndsWith(postfix string) ([]TOTPPhone, error) {
	postfix = "%" + postfix
	rows, err := s.db.Query("SELECT phone, created_at_ms FROM totp WHERE phone LIKE ?", postfix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	phones := []TOTPPhone{}
	for rows.Next() {
		phone := TOTPPhone{}
		err = rows.Scan(&phone.Phone, &phone.CreatedAtMilli)
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
