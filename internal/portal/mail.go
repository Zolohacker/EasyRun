package portal

import (
	"context"
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"time"
)

// SendMail supports plain SMTP relays, implicit TLS (SMTPS), and mandatory
// STARTTLS. Certificates are always verified; STARTTLS never downgrades.
func SendMail(ctx context.Context, c MailConfig, to, subject, body string) error {
	if _, e := normalizedEmail(c.From); e != nil {
		return e
	}
	if _, e := normalizedEmail(to); e != nil {
		return e
	}
	address := net.JoinHostPort(c.Host, fmt.Sprint(c.Port))
	d := &net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	var e error
	tlsConfig := &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}
	switch c.Protocol {
	case "smtps":
		conn, e = (&tls.Dialer{NetDialer: d, Config: tlsConfig}).DialContext(ctx, "tcp", address)
	case "smtp", "starttls":
		conn, e = d.DialContext(ctx, "tcp", address)
	default:
		return fmt.Errorf("unsupported mail protocol")
	}
	if e != nil {
		return e
	}
	defer conn.Close()
	deadline := time.Now().Add(20 * time.Second)
	if v, ok := ctx.Deadline(); ok && v.Before(deadline) {
		deadline = v
	}
	_ = conn.SetDeadline(deadline)
	client, e := smtp.NewClient(conn, c.Host)
	if e != nil {
		return e
	}
	defer client.Close()
	if c.Protocol == "starttls" {
		if e = client.StartTLS(tlsConfig); e != nil {
			return fmt.Errorf("STARTTLS: %w", e)
		}
	}
	if c.Username != "" {
		if e = client.Auth(smtp.PlainAuth("", c.Username, c.Password, c.Host)); e != nil {
			return e
		}
	}
	if e = client.Mail(c.From); e != nil {
		return e
	}
	if e = client.Rcpt(to); e != nil {
		return e
	}
	writer, e := client.Data()
	if e != nil {
		return e
	}
	_, e = fmt.Fprintf(writer, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n", c.From, to, mime.QEncoding.Encode("UTF-8", subject), body)
	if e != nil {
		return e
	}
	if e = writer.Close(); e != nil {
		return e
	}
	return client.Quit()
}
