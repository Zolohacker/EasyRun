package portal

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func smtpFixture(t *testing.T) (MailConfig, <-chan string) {
	t.Helper()
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { listener.Close() })
	messages := make(chan string, 1)
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		reader := bufio.NewReader(conn)
		fmt.Fprint(conn, "220 localhost ESMTP\r\n")
		for {
			line, e := reader.ReadString('\n')
			if e != nil {
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				fmt.Fprint(conn, "250 localhost\r\n")
			case strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
				fmt.Fprint(conn, "250 OK\r\n")
			case strings.HasPrefix(line, "DATA"):
				fmt.Fprint(conn, "354 Send data\r\n")
				var body strings.Builder
				for {
					line, e = reader.ReadString('\n')
					if e != nil {
						return
					}
					if line == ".\r\n" {
						break
					}
					body.WriteString(line)
				}
				messages <- body.String()
				fmt.Fprint(conn, "250 queued\r\n")
			case strings.HasPrefix(line, "QUIT"):
				fmt.Fprint(conn, "221 bye\r\n")
				return
			default:
				fmt.Fprint(conn, "502 Unsupported command\r\n")
			}
		}
	}()
	return MailConfig{Protocol: "smtp", Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, From: "sender@example.test"}, messages
}
func TestSMTPDeliveryAndNoStartTLSDowngrade(t *testing.T) {
	cfg, messages := smtpFixture(t)
	if e := SendMail(context.Background(), cfg, "receiver@example.test", "测试邮件", "123456"); e != nil {
		t.Fatal(e)
	}
	select {
	case body := <-messages:
		if !strings.Contains(body, "123456") || !strings.Contains(body, "charset=UTF-8") {
			t.Fatal("message lost")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no message received")
	}
	cfg, _ = smtpFixture(t)
	cfg.Protocol = "starttls"
	if e := SendMail(context.Background(), cfg, "receiver@example.test", "test", "secret"); e == nil {
		t.Fatal("STARTTLS silently downgraded")
	}
}
