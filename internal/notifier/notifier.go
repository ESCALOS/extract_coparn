package notifier

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

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

type loginAuth struct {
	username string
	password string
}

func New(cfg config.EmailConfig) Notifier {
	if !cfg.Enabled {
		return Noop{}
	}
	return &SMTPNotifier{cfg: cfg}
}

func (n *SMTPNotifier) Send(subject, body string) error {
	if n.cfg.Host == "" || n.cfg.From == "" || n.cfg.To == "" {
		return fmt.Errorf("configuracion SMTP incompleta")
	}
	timeout := n.cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	addr := fmt.Sprintf("%s:%d", n.cfg.Host, n.cfg.Port)
	conn, err := (&net.Dialer{Timeout: timeout}).Dial("tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	c, err := smtp.NewClient(conn, n.cfg.Host)
	if err != nil {
		return err
	}
	defer c.Close()

	if ok, _ := c.Extension("STARTTLS"); ok {
		tlsCfg := &tls.Config{ServerName: n.cfg.Host, MinVersion: tls.VersionTLS12}
		if err := c.StartTLS(tlsCfg); err != nil {
			return err
		}
	}
	if n.cfg.User != "" {
		if err := c.Auth(n.auth(c)); err != nil {
			return err
		}
	}
	if err := c.Mail(n.cfg.From); err != nil {
		return err
	}
	for _, recipient := range splitRecipients(n.cfg.To) {
		if err := c.Rcpt(recipient); err != nil {
			return err
		}
	}

	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(message(n.cfg.From, n.cfg.To, subject, body))); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func (n *SMTPNotifier) auth(c *smtp.Client) smtp.Auth {
	if ok, mechanisms := c.Extension("AUTH"); ok {
		if strings.Contains(strings.ToUpper(mechanisms), "LOGIN") {
			return loginAuth{username: n.cfg.User, password: n.cfg.Password}
		}
	}
	return smtp.PlainAuth("", n.cfg.User, n.cfg.Password, n.cfg.Host)
}

func (a loginAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", nil, nil
}

func (a loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	challenge := strings.ToLower(string(fromServer))
	if strings.Contains(challenge, "user") {
		return []byte(a.username), nil
	}
	return []byte(a.password), nil
}

func message(from, to, subject, body string) string {
	return "From: " + from + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"\r\n" + body + "\r\n"
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
