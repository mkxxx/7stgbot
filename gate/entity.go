package gate

import (
	"database/sql"
	"time"
)

const createEntities string = `
  CREATE TABLE IF NOT EXISTS entities (
  tp TEXT NOT NULL,
  id TEXT NOT NULL,
  data TEXT NOT NULL,
  created INT NOT NULL,
  updated INT NOT NULL,
  PRIMARY KEY (tp, id)
  );`

type Entities struct {
	db *sql.DB
}

type Entity interface {
	Type() string
	ID() string
	MarshalData() (string, error)
	UnmarshalData(data string) error
}

type EntitiesDAO interface {
	Load(p Entity) (ok bool, err error)
	Insert(p Entity) error
	Update(p Entity) error
	Delete(p Entity) error
}

func NewEntities(db *sql.DB) EntitiesDAO {
	if db == nil {
		return &NullEntities{}
	}
	if _, err := db.Exec(createEntities); err != nil {
		Logger.Errorf("creating table entities %v", err)
		return &NullEntities{}
	}
	return &Entities{
		db: db,
	}
}

func (s *Entities) Insert(p Entity) error {
	data, err := p.MarshalData()
	if err != nil {
		return err
	}
	created := time.Now().Unix()
	_, err = s.db.Exec("INSERT OR IGNORE INTO entities (tp, id, data, created, updated) VALUES(?,?,?,?,?);",
		p.Type(), p.ID(), data, created, created)
	return err
}

func (s *Entities) Update(p Entity) error {
	data, err := p.MarshalData()
	if err != nil {
		return err
	}
	updated := time.Now().Unix()
	_, err = s.db.Exec("UPDATE entities SET data = ?, updated = ? WHERE tp = ? AND id = ?;",
		data, updated, p.Type(), p.ID())
	if err != nil {
		return err
	}
	return nil
}

func (s *Entities) Delete(p Entity) error {
	_, err := s.db.Exec("DELETE FROM entities WHERE tp = ? AND id = ?;",
		p.Type(), p.ID())
	return err
}

func (s *Entities) Load(p Entity) (ok bool, err error) {
	rows, err := s.db.Query("SELECT data FROM entities WHERE tp = ? AND id = ?", p.Type(), p.ID())
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var data string
		err = rows.Scan(&data)
		if err != nil {
			Logger.Errorf("scan %s:%s error: %v", p.Type(), p.ID(), err)
			return false, err
		}
		err := p.UnmarshalData(data)
		if err != nil {
			Logger.Errorf("unmarshal %s:%s %s error: %v", p.Type(), p.ID(), data, err)
			return false, err
		}
		return true, nil //lint:ignore
	}
	return false, nil
}

type NullEntities struct {
}

func (s *NullEntities) Load(p Entity) (ok bool, err error) {
	return false, nil
}

func (s *NullEntities) Insert(p Entity) error {
	return nil
}

func (s *NullEntities) Update(p Entity) error {
	return nil
}

func (s *NullEntities) Delete(p Entity) error {
	return nil
}
