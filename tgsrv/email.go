package tgsrv

import (
	"gopkg.in/gomail.v2"
	"log"
)

type emailClient struct {
	username string
	password string
	from     string
}

func (c *emailClient) sendEmail(email string, subject string, body string) {
	msg := gomail.NewMessage()
	//msg.SetHeader("From", "mizzgan+ifttt@gmail.com")
	msg.SetHeader("From", c.from)
	//msg.SetHeader("To", "trigger@recipe.ifttt.com")
	msg.SetHeader("To", email)
	msg.SetHeader("Subject", subject)
	msg.SetHeader("X-Mailer", "Microsoft Office Outlook, Build 12.0.4210")
	msg.SetBody("text/html", body)
	//msg.Attach("/home/User/cat.jpg")

	n := gomail.NewDialer("smtp.gmail.com", 587, c.username, c.password)

	// Send the email
	if err := n.DialAndSend(msg); err != nil {
		log.Printf("error sending email smtp.gmail.com:587 phone: %q sms: %q username: %q, %v",
			subject, body, c.username, err)
	}
}
