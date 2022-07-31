package tgsrv

import (
	"database/sql"
	_ "github.com/mattn/go-sqlite3"
	"time"
)

const createUsers string = `
  CREATE TABLE IF NOT EXISTS users (
  email TEXT NOT NULL PRIMARY KEY,
  chat_id INTEGER NOT NULL,
  created_at int NOT NULL,
  updated_at int,
  updated int                              
  );`

const file string = "users.db"

type Users struct {
	db *sql.DB
}

type User struct {
	ChatID    int64
	Email     string
	CreatedAt int64
}

func NewUsers() (*Users, error) {
	db, err := sql.Open("sqlite3", file)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(createUsers); err != nil {
		return nil, err
	}
	return &Users{
		db: db,
	}, nil
}

func (u *Users) Insert(p User) error {
	/*
		INSERT INTO phonebook2(name,phonenumber,validDate)
		  VALUES('Alice','704-555-1212','2018-05-08')
		  ON CONFLICT(name) DO UPDATE SET
		    phonenumber=excluded.phonenumber,
		    validDate=excluded.validDate
	*/
	now := time.Now().Unix()
	_, err := u.db.Exec("INSERT INTO users (email, chat_id, created_at) VALUES(?,?,?)"+
		" ON CONFLICT(email) DO UPDATE SET chat_id = ?, updated_at = ?, updated = updated + 1;",
		p.Email, p.ChatID, now,
		p.ChatID, now)
	if err != nil {
		return err
	}
	return nil
}

func (u *Users) List() ([]User, error) {
	//rows, err := c.db.Query("SELECT * FROM users WHERE ID > ? ORDER BY id DESC LIMIT 100", offset)
	rows, err := u.db.Query("SELECT email, chat_id, created_at FROM users ORDER BY email")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := []User{}
	for rows.Next() {
		u := User{}
		err = rows.Scan(&u.Email, &u.ChatID, &u.CreatedAt)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, nil
}

func (u *Users) user(chatID int64) *User {
	users, err := u.List()
	if err != nil {
		Logger.Errorf("error selecting users from db %v", err)
		return nil
	}
	for _, user := range users {
		if user.ChatID == chatID {
			return &user
		}
	}
	return nil
}
