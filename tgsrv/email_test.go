package tgsrv

import (
	"testing"
)

func TestSendEmail2SMS(t *testing.T) {
	c := emailClient{username: "aaa@gmail.com", password: "aaa", from: "aaa@gmail.com"}
	c.sendEmail("trigger@recipe.ifttt.com", "+79152288715", "ку-ку")
}
