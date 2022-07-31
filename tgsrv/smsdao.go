package tgsrv

import (
	"database/sql"
	"time"
)

const createSMSes string = `
  CREATE TABLE IF NOT EXISTS smses (
  id INTEGER PRIMARY KEY,     
  phone TEXT NOT NULL,
  created_at int NOT NULL,                           
  sent_at int,                           
  msg TEXT NOT NULL
  );`

const smsFile string = "smses.db"

type SMSes struct {
	db *sql.DB
}

type SMS struct {
	ID        int
	Phone     string
	Msg       string
	CreatedAt int64
	SentAt    int64
}

func NewSMSes() (*SMSes, error) {
	db, err := sql.Open("sqlite3", smsFile)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(createSMSes); err != nil {
		return nil, err
	}
	return &SMSes{
		db: db,
	}, nil
}

func (u *SMSes) Insert(p SMS) error {
	_, err := u.db.Exec("INSERT INTO smses (phone, created_at, msg) VALUES(?,?,?);",
		p.Phone, p.CreatedAt, p.Msg)
	if err != nil {
		return err
	}
	return nil
}

func (u *SMSes) Update(p SMS) error {
	now := time.Now().UnixMilli()
	_, err := u.db.Exec("UPDATE smses SET sent_at = ? WHERE ID = ?;",
		now, p.ID)
	if err != nil {
		return err
	}
	return nil
}

func (u *SMSes) ListNew() ([]SMS, error) {
	rows, err := u.db.Query("SELECT id, phone, created_at, msg FROM smses WHERE sent_at IS NULL ORDER BY created_at LIMIT 100")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	smses := []SMS{}
	for rows.Next() {
		sms := SMS{}
		err = rows.Scan(&sms.ID, &sms.Phone, &sms.CreatedAt, &sms.Msg)
		if err != nil {
			return nil, err
		}
		smses = append(smses, sms)
	}
	return smses, nil
}
