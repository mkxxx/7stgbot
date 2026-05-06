package gate

import (
	"database/sql"
	"strconv"
)

const createSettings string = `
  CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
  );`

type Settings struct {
	db *sql.DB
}

type Setting struct {
	Key   string
	value string
}

func (s *Setting) ValueString() string {
	return s.value
}

func (s *Setting) SetString(p string) {
	s.value = p
}

func (s *Setting) ValueBool(def bool) bool {
	if s.value == "" {
		return def
	}
	v, err := strconv.ParseBool(s.value)
	if err != nil {
		Logger.Errorf("setting %q parse %q error: ", s.Key, s.value, err)
		return def
	}
	return v
}

func (s *Setting) SetBool(p bool) {
	s.value = strconv.FormatBool(p)
}

type SettingsDAO interface {
	Find(string) (*Setting, error)
	Update(p *Setting) error
}

func NewSettings(db *sql.DB) SettingsDAO {
	if db == nil {
		return &NullSettings{}
	}
	if _, err := db.Exec(createSettings); err != nil {
		Logger.Errorf("creating table settings %v", err)
		return &NullSettings{}
	}
	return &Settings{
		db: db,
	}
}

func (s *Settings) Update(p *Setting) error {
	_, err := s.db.Exec("INSERT INTO settings (key, value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value = excluded.value;",
		p.Key, p.value)

	if err != nil {
		Logger.Errorf("db upsert settings (%q,%q) error: %v", p.Key, p.value, err)
	}
	return err
}

func (s *Settings) Find(key string) (*Setting, error) {
	rows, err := s.db.Query("SELECT key, value FROM settings WHERE key = ?", key)
	if err != nil {
		Logger.Errorf("db select from settings %q error: %v", key, err)
		return nil, err
	}
	defer rows.Close()

	settings := []Setting{}
	for rows.Next() {
		s := Setting{}
		err = rows.Scan(&s.Key, &s.value)
		if err != nil {
			return nil, err
		}
		settings = append(settings, s)
	}
	if len(settings) == 0 {
		return &Setting{Key: key}, nil
	}
	return &settings[0], nil
}

type NullSettings struct {
}

func (s *NullSettings) Find(key string) (*Setting, error) {
	return &Setting{Key: key}, nil
}

func (s *NullSettings) Update(p *Setting) error {
	return nil
}
