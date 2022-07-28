package tgsrv

import (
	"testing"
)

func TestSendEmail2SMS(t *testing.T) {
	c := EmailClient{username: "aaa@gmail.com", password: "aaa", from: "aaa@gmail.com"}
	c.sendEmail("trigger@recipe.ifttt.com", "+79152288715", "ку-ку")
	//c.sendEmail("pishnuta@yandex.ru", "+79152288715", "ку-ку")
}
