package gate

import (
	"database/sql"
)

const createMMUsers string = `
  CREATE TABLE IF NOT EXISTS mm_users (
  mm_user_id TEXT PRIMARY KEY,
  phone TEXT NOT NULL
  );`

type MattermostUsers struct {
	db *sql.DB
}

type MattermostUser struct {
	UserId string
	Phone  string
}

type MattermostUsersDAO interface {
	Find(string) (*MattermostUser, error)
	Insert(p *MattermostUser) error
	Update(p *MattermostUser) error
}

func NewMattermostUsers(db *sql.DB) MattermostUsersDAO {
	if db == nil {
		return &NullMattermostUsers{}
	}
	if _, err := db.Exec(createMMUsers); err != nil {
		Logger.Errorf("creating table mm_users %v", err)
		return &NullMattermostUsers{}
	}
	return &MattermostUsers{
		db: db,
	}
}

func (s *MattermostUsers) Insert(p *MattermostUser) error {
	_, err := s.db.Exec("INSERT OR IGNORE INTO mm_users (mm_user_id, phone) VALUES(?,?);",
		p.UserId, p.Phone)
	return err
}

func (s *MattermostUsers) Update(p *MattermostUser) error {
	_, err := s.db.Exec("UPDATE mm_users SET phone = ? WHERE mm_user_id = ?;",
		p.Phone, p.UserId)
	if err != nil {
		return err
	}
	return nil
}

func (s *MattermostUsers) Find(userId string) (*MattermostUser, error) {
	rows, err := s.db.Query("SELECT mm_user_id, phone FROM mm_users WHERE mm_user_id = ?", userId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := []MattermostUser{}
	for rows.Next() {
		phone := MattermostUser{}
		err = rows.Scan(&phone.UserId, &phone.Phone)
		if err != nil {
			return nil, err
		}
		users = append(users, phone)
	}
	if len(users) == 0 {
		return nil, nil
	}
	return &users[0], nil
}

type NullMattermostUsers struct {
}

func (s *NullMattermostUsers) Find(postfix string) (*MattermostUser, error) {
	return nil, nil
}

func (s *NullMattermostUsers) Insert(p *MattermostUser) error {
	return nil
}

func (s *NullMattermostUsers) Update(p *MattermostUser) error {
	return nil
}
