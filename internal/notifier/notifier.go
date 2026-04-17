package notifier

import (
	"fmt"
	"net/smtp"
	"strings"

	"extract_coparn/internal/config"
)

type Notifier interface {
	Send(subject, body string) error
}

type Noop struct{}

func (Noop) Send(subject, body string) error { return nil }

type SMTPNotifier struct {
	cfg config.EmailConfig
}

func New(cfg config.EmailConfig) Notifier {
	if !cfg.Enabled {
		return Noop{}
	}
	return &SMTPNotifier{cfg: cfg}
}

func (n *SMTPNotifier) Send(subject, body string) error {
	if n.cfg.Host == "" || n.cfg.From == "" || n.cfg.To == "" {
		return fmt.Errorf("configuración SMTP incompleta")
	}
	addr := fmt.Sprintf("%s:%d", n.cfg.Host, n.cfg.Port)
	auth := smtp.PlainAuth("", n.cfg.User, n.cfg.Password, n.cfg.Host)
	msg := "From: " + n.cfg.From + "\r\n" +
		"To: " + n.cfg.To + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"\r\n" + body + "\r\n"
	to := splitRecipients(n.cfg.To)
	return smtp.SendMail(addr, auth, n.cfg.From, to, []byte(msg))
}

func splitRecipients(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
