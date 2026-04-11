package gate

import (
	"database/sql"
	"time"

	"go.uber.org/zap"
)

var Logger *zap.SugaredLogger

const createSMSes string = `
  CREATE TABLE IF NOT EXISTS sms (
  id INTEGER PRIMARY KEY,
  phone TEXT NOT NULL,
  created_at_ms int NOT NULL,
  deadline_ms int NOT NULL,
  sent_at_ms int,
  msg TEXT NOT NULL
  );`

const smsFile string = "sms.db"

type SMSes struct {
	db *sql.DB
}

type SMS struct {
	ID             int
	Phone          string
	Msg            string
	CreatedAtMilli int64
	DeadlineMilli  int64
	SentAtMilli    int64
}

func (s *SMS) Sent() {
	s.SentAtMilli = time.Now().UnixMilli()
}

func (s *SMS) Expired() bool {
	return s.DeadlineMilli < time.Now().UnixMilli()
}

type Call struct {
	Phone     string
	CreatedAt int64
	Deadline  int64
	CalledAt  int64
}

type SMSesDAO interface {
	ListNew(int) ([]SMS, error)
	Insert(p *SMS) error
	Update(p *SMS) error
}

func NewSMSes() SMSesDAO {
	db, err := sql.Open("sqlite3", smsFile)
	if err != nil {
		Logger.Errorf("opening %s %v", smsFile, err)
		return &NullSMSes{}
	}
	if _, err := db.Exec(createSMSes); err != nil {
		Logger.Errorf("creating table %s %v", smsFile, err)
		return &NullSMSes{}
	}
	return &SMSes{
		db: db,
	}
}

func (s *SMSes) Insert(p *SMS) error {
	_, err := s.db.Exec("INSERT INTO sms (phone, created_at_ms, deadline_ms, msg) VALUES(?,?,?,?);",
		p.Phone, p.CreatedAtMilli, p.DeadlineMilli, p.Msg)
	if err != nil {
		return err
	}
	return nil
}

func (s *SMSes) Update(p *SMS) error {
	_, err := s.db.Exec("UPDATE sms SET sent_at_ms = ? WHERE ID = ?;",
		p.SentAtMilli, p.ID)
	if err != nil {
		return err
	}
	return nil
}

func (s *SMSes) ListNew(n int) ([]SMS, error) {
	now := time.Now().UnixMilli()
	rows, err := s.db.Query("SELECT id, phone, created_at_ms, deadline_ms, msg FROM sms WHERE sent_at_ms IS NULL AND deadline_ms > ? ORDER BY created_at_ms LIMIT ?", now, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	smses := []SMS{}
	for rows.Next() {
		sms := SMS{}
		err = rows.Scan(&sms.ID, &sms.Phone, &sms.CreatedAtMilli, &sms.DeadlineMilli, &sms.Msg)
		if err != nil {
			return nil, err
		}
		smses = append(smses, sms)
	}
	return smses, nil
}

type NullSMSes struct {
}

func (s *NullSMSes) ListNew(n int) ([]SMS, error) {
	return nil, nil
}

func (s *NullSMSes) Insert(p *SMS) error {
	return nil
}

func (s *NullSMSes) Update(p *SMS) error {
	return nil
}
