// Package notify 通过 SMTP 发送邮件。
package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// 加密方式，与设置中的取值一致。
const (
	SecurityNone     = "none"
	SecuritySTARTTLS = "starttls"
	SecurityTLS      = "tls"
)

// Timeout 是一次发送的等待上限。
const Timeout = 20 * time.Second

// Config 是 SMTP 服务器和收发件人。
type Config struct {
	Host     string
	Port     int
	Security string
	Username string
	Password string
	From     string
	To       []string
}

// Send 发送一封纯文本邮件。
func Send(ctx context.Context, config Config, subject, body string) error {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	from, err := mail.ParseAddress(config.From)
	if err != nil {
		return fmt.Errorf("invalid sender: %w", err)
	}
	if len(config.To) == 0 {
		return errors.New("no recipients")
	}
	recipients := make([]string, 0, len(config.To))
	for _, to := range config.To {
		address, err := mail.ParseAddress(to)
		if err != nil {
			return fmt.Errorf("invalid recipient %q: %w", to, err)
		}
		recipients = append(recipients, address.Address)
	}
	addr := net.JoinHostPort(config.Host, strconv.Itoa(config.Port))
	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	tlsConfig := &tls.Config{ServerName: config.Host, MinVersion: tls.VersionTLS12}
	if config.Security == SecurityTLS {
		conn = tls.Client(conn, tlsConfig)
	}
	client, err := smtp.NewClient(conn, config.Host)
	if err != nil {
		return err
	}
	defer client.Close()
	if config.Security == SecuritySTARTTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("the server does not support STARTTLS")
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return err
		}
	}
	if config.Username != "" {
		// net/smtp 的 PlainAuth 拒绝在非加密连接上发送密码，这里保持这一行为。
		if err := client.Auth(smtp.PlainAuth("", config.Username, config.Password, config.Host)); err != nil {
			return err
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return err
	}
	for _, to := range recipients {
		if err := client.Rcpt(to); err != nil {
			return err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(Message(config.From, config.To, subject, body, time.Now())); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

// Message 组装邮件内容：主题按 RFC 2047 编码，正文 UTF-8 + base64。
func Message(from string, to []string, subject, body string, at time.Time) []byte {
	var out bytes.Buffer
	header := func(name, value string) { fmt.Fprintf(&out, "%s: %s\r\n", name, sanitizeHeader(value)) }
	header("From", from)
	header("To", strings.Join(to, ", "))
	header("Subject", mime.QEncoding.Encode("utf-8", subject))
	header("Date", at.Format(time.RFC1123Z))
	header("MIME-Version", "1.0")
	header("Content-Type", "text/plain; charset=utf-8")
	header("Content-Transfer-Encoding", "base64")
	out.WriteString("\r\n")
	encoded := base64.StdEncoding.EncodeToString([]byte(body))
	for len(encoded) > 76 {
		out.WriteString(encoded[:76] + "\r\n")
		encoded = encoded[76:]
	}
	out.WriteString(encoded + "\r\n")
	return out.Bytes()
}

// sanitizeHeader 去掉换行，防止用户名等内容注入邮件头。
func sanitizeHeader(value string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
}
