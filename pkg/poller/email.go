package poller

import (
	"fmt"
	"net/smtp"

	"github.com/sendgrid/rest"
	"github.com/sendgrid/sendgrid-go/helpers/mail"
)

type EmailSender interface {
	Send(email *mail.SGMailV3) (*rest.Response, error)
}

type SMTPSender struct {
	Addr string
}

func (s *SMTPSender) Send(message *mail.SGMailV3) (*rest.Response, error) {
	from := message.From.Address
	to := ""
	if len(message.Personalizations) > 0 && len(message.Personalizations[0].To) > 0 {
		to = message.Personalizations[0].To[0].Address
	}
	subject := message.Subject
	var body string
	var cType string
	for _, c := range message.Content {
		if c.Type == "text/html" {
			body = c.Value
			cType = "text/html"
			break
		}
	}
	if body == "" && len(message.Content) > 0 {
		body = message.Content[0].Value
		cType = message.Content[0].Type
	}

	msg := fmt.Appendf(nil, "To: %s\r\n"+
		"From: %s\r\n"+
		"Subject: %s\r\n"+
		"Content-Type: %s; charset=UTF-8\r\n"+
		"\r\n"+
		"%s\r\n", to, from, subject, cType, body)

	err := smtp.SendMail(s.Addr, nil, from, []string{to}, msg)
	if err != nil {
		return nil, err
	}
	return &rest.Response{StatusCode: 202}, nil
}
