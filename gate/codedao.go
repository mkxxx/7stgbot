package gate

import (
	"database/sql"
	"time"
)

const createKeypadCodes string = `
  CREATE TABLE IF NOT EXISTS kpcodes (
  id INTEGER PRIMARY KEY,
  code TEXT NOT NULL,
  req_phone TEXT NOT NULL,
  created_at_ms int NOT NULL,
  end_time_ms int,
  ttl_min int
  );`

type KeypadCodes struct {
	db *sql.DB
}

// TODO add created time
type KeypadCode struct {
	ID               int
	Code             string
	RequesterPhone   string
	CreatedTimeMilli int64
	EndTimeMilli     int64
	TTLMinutes       int
}

func (s *KeypadCode) Temporal() bool {
	if s.TTLMinutes <= 60*24*3 {
		return true
	}
	// TODO add created time
	return false
}

func (s *KeypadCode) Expired() bool {
	return s.EndTimeMilli != 0 && s.EndTimeMilli < time.Now().UnixMilli()
}

type KeypadCodesDAO interface {
	ListActive() ([]KeypadCode, error)
	Insert(p *KeypadCode) error
	Update(p *KeypadCode) error
}

func NewKeypadCodes(db *sql.DB) KeypadCodesDAO {
	if db == nil {
		return &NullKeypadCodes{}
	}
	if _, err := db.Exec(createKeypadCodes); err != nil {
		Logger.Errorf("creating table kpcodes %v", err)
		return &NullKeypadCodes{}
	}
	return &KeypadCodes{
		db: db,
	}
}

func (s *KeypadCodes) Insert(p *KeypadCode) error {
	_, err := s.db.Exec("INSERT INTO kpcodes (code, req_phone, created_at_ms, end_time_ms, ttl_min) VALUES(?,?,?,?,?);",
		p.Code, p.RequesterPhone, p.CreatedTimeMilli, p.EndTimeMilli, p.TTLMinutes)
	if err != nil {
		return err
	}
	return nil
}

func (s *KeypadCodes) Update(p *KeypadCode) error {
	_, err := s.db.Exec("UPDATE kpcodes SET end_time_ms = ? WHERE ID = ?;",
		p.EndTimeMilli, p.ID)
	if err != nil {
		return err
	}
	return nil
}

func (s *KeypadCodes) ListActive() ([]KeypadCode, error) {
	now := time.Now().UnixMilli()
	rows, err := s.db.Query("SELECT id, code, req_phone, created_at_ms, end_time_ms, ttl_min FROM kpcodes WHERE end_time_ms > ? || end_time_ms == 0", now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	codes := []KeypadCode{}
	for rows.Next() {
		code := KeypadCode{}
		err = rows.Scan(&code.ID, &code.Code, &code.RequesterPhone, &code.CreatedTimeMilli, &code.EndTimeMilli, &code.TTLMinutes)
		if err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	return codes, nil
}

type NullKeypadCodes struct {
}

func (s *NullKeypadCodes) ListActive() ([]KeypadCode, error) {
	return nil, nil
}

func (s *NullKeypadCodes) Insert(p *KeypadCode) error {
	return nil
}

func (s *NullKeypadCodes) Update(p *KeypadCode) error {
	return nil
}

func Find(dao KeypadCodesDAO, code string) (*KeypadCode, error) {
	codes, err := dao.ListActive()
	if err != nil {
		return nil, err
	}
	for _, c := range codes {
		if c.Code == code {
			return &c, nil
		}
	}
	return nil, nil
}
