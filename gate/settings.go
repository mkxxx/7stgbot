package gate

import (
	"bytes"
	"database/sql"
	"log"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const ScheduledSettingsKeyPrefix = "mm-daily."

var Location *time.Location

func init() {
	var err error
	Location, err = time.LoadLocation("Europe/Moscow")
	if err != nil {
		log.Fatal(err)
	}
}

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

func (s *Setting) ValueInt(def int) int {
	if s.value == "" {
		return def
	}
	v, err := strconv.Atoi(s.value)
	if err != nil {
		Logger.Errorf("setting %q parse %q error: ", s.Key, s.value, err)
		return def
	}
	return v
}

func (s *Setting) SetInt(p int) {
	s.value = strconv.Itoa(p)
}

func (s *Setting) ValueFloat(def float64) float64 {
	if s.value == "" {
		return def
	}
	v, err := strconv.ParseFloat(s.value, 64)
	if err != nil {
		Logger.Errorf("setting %q parse %q error: ", s.Key, s.value, err)
		return def
	}
	return v
}

func (s *Setting) SetFloat(p float64) {
	s.value = strconv.FormatFloat(p, 'f', -1, 64)
}

func (s *Setting) Validate() (err error) {
	if s.value == "" {
		return nil
	}
	n := len(s.Key)
	if n < 3 {
		return nil
	}
	suffex := s.Key[n-2:]
	switch suffex {
	case ".b":
		_, err = strconv.ParseBool(s.value)
	case ".i":
		_, err = strconv.Atoi(s.value)
	case ".f":
		_, err = strconv.ParseFloat(s.value, 64)
	}
	if err != nil {
		return err
	}
	if s.IsScheduled() {
		_, err = s.Schedule()
	}
	return err
}

func (s *Setting) IsScheduled() bool {
	return strings.HasPrefix(s.Key, ScheduledSettingsKeyPrefix)
}

func (s *Setting) Schedule() (*Schedule, error) {
	var sch Schedule
	err := UnmarshalYAMLOneLine(s.value, &sch)
	return &sch, err
}

func UnmarshalYAMLOneLine(p string, v any) error {
	decoder := yaml.NewDecoder(bytes.NewReader([]byte(p)))
	decoder.KnownFields(true)
	return decoder.Decode(v)
}

func MarshalYAMLOneLine(v any) string {
	var sb strings.Builder
	encoder := yaml.NewEncoder(&sb)
	encoder.SetIndent(0)
	encoder.Encode(v)
	return "{" + strings.TrimSuffix(strings.ReplaceAll(sb.String(), "\n", ","), ",") + "}"
}

type SettingsDAO interface {
	Find(string) (*Setting, error)
	FindN(string) (*[]Setting, error)
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

func (s *Settings) FindN(key string) (*[]Setting, error) {
	rows, err := s.db.Query("SELECT key, value FROM settings WHERE key like ?", key+"%")
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
	return &settings, nil
}

type NullSettings struct {
}

func (s *NullSettings) Find(key string) (*Setting, error) {
	return &Setting{Key: key}, nil
}

func (s *NullSettings) FindN(key string) (*[]Setting, error) {
	return nil, nil
}

func (s *NullSettings) Update(p *Setting) error {
	return nil
}

type Schedule struct {
	From          string `yaml:"from"`
	To            string `yaml:"to"`
	Args          string `yaml:"args,omitempty"`
	ExecTimeMilli int64  `yaml:"execTimeMilli,omitempty"`
	ExecError     string `yaml:"execError,omitempty"`
	ExecResult    string `yaml:"execResult,omitempty"`
}

func (s *Schedule) IsValid() bool {
	from, to, _ := s.PeriodContaining(time.Unix(1782378000, 0), true)
	return !from.Equal(to)
}

func (s *Schedule) IsTime(t time.Time) bool {
	from, to, ok := s.PeriodContaining(t, true)
	if !ok {
		return ok
	}
	if s.ExecTimeMilli == 0 {
		return true
	}
	lastTime := time.UnixMilli(s.ExecTimeMilli)
	return from.After(lastTime) || !to.After(lastTime)
}

func (s *Schedule) PeriodContaining(t time.Time, orBefore bool) (from, to time.Time, ok bool) {
	from = withTime(t, s.From)
	to = withTime(t, s.To)
	if to.Hour() == 0 && to.Minute() == 0 && to.Second() == 0 {
		to = to.AddDate(0, 0, 1)
	}
	containsMidnight := from.After(to)
	if containsMidnight {
		from = from.AddDate(0, 0, -1)
	}
	ok = !from.After(t) && to.After(t)
	if ok {
		return
	}
	if containsMidnight {
		from2 := from.AddDate(0, 0, 1)
		to2 := to.AddDate(0, 0, 1)
		ok = !from2.After(t) && to2.After(t)
		if ok || orBefore {
			return from2, to2, ok
		}
		return
	}
	after := from.After(t)
	if orBefore {
		if after {
			from = from.AddDate(0, 0, -1)
			to = to.AddDate(0, 0, -1)
		}
	} else if !after {
		from = from.AddDate(0, 0, 1)
		to = to.AddDate(0, 0, 1)
	}
	return
}

func withTime(t time.Time, tm string) time.Time {
	if tm == "" {
		return t
	}
	hh, mm, ss := 0, 0, 0
	hhmmss := strings.Split(tm, ":")
	n := len(hhmmss)
	if n >= 1 {
		hh, _ = strconv.Atoi(hhmmss[0])
	}
	if n >= 2 {
		mm, _ = strconv.Atoi(hhmmss[1])
	}
	if n >= 3 {
		ss, _ = strconv.Atoi(hhmmss[2])
	}
	return time.Date(t.Year(), t.Month(), t.Day(), hh, mm, ss, 0, Location)
}
