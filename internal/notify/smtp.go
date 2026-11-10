// Package notify 通过 SMTP 发送邮件。
package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
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

// Attachment 是邮件附件。
type Attachment struct {
	Name        string
	ContentType string
	Data        []byte
}

// Send 发送一封纯文本邮件，可以带附件。
func Send(ctx context.Context, config Config, subject, body string, attachments ...Attachment) error {
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
	if _, err := writer.Write(Message(config.From, config.To, subject, body, time.Now(), attachments...)); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

// Message 组装邮件内容：主题按 RFC 2047 编码，正文 UTF-8 + base64。有附件时整封邮件为 multipart/mixed。
func Message(from string, to []string, subject, body string, at time.Time, attachments ...Attachment) []byte {
	var out bytes.Buffer
	header := func(name, value string) { fmt.Fprintf(&out, "%s: %s\r\n", name, sanitizeHeader(value)) }
	header("From", from)
	header("To", strings.Join(to, ", "))
	header("Subject", mime.QEncoding.Encode("utf-8", subject))
	header("Date", at.Format(time.RFC1123Z))
	header("MIME-Version", "1.0")
	if len(attachments) == 0 {
		header("Content-Type", "text/plain; charset=utf-8")
		header("Content-Transfer-Encoding", "base64")
		out.WriteString("\r\n")
		writeBase64(&out, []byte(body))
		return out.Bytes()
	}
	parts := multipart.NewWriter(&out)
	header("Content-Type", mime.FormatMediaType("multipart/mixed", map[string]string{"boundary": parts.Boundary()}))
	out.WriteString("\r\n")
	part, _ := parts.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"text/plain; charset=utf-8"},
		"Content-Transfer-Encoding": {"base64"},
	})
	writeBase64(part, []byte(body))
	for _, attachment := range attachments {
		contentType := attachment.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		name := sanitizeHeader(attachment.Name)
		part, _ := parts.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {mime.FormatMediaType(contentType, map[string]string{"name": name})},
			"Content-Disposition":       {mime.FormatMediaType("attachment", map[string]string{"filename": name})},
			"Content-Transfer-Encoding": {"base64"},
		})
		writeBase64(part, attachment.Data)
	}
	_ = parts.Close()
	return out.Bytes()
}

// writeBase64 按每行 76 个字符写入 base64 编码的内容。
func writeBase64(out io.Writer, data []byte) {
	encoded := base64.StdEncoding.EncodeToString(data)
	for len(encoded) > 76 {
		_, _ = io.WriteString(out, encoded[:76]+"\r\n")
		encoded = encoded[76:]
	}
	_, _ = io.WriteString(out, encoded+"\r\n")
}

// sanitizeHeader 去掉换行，防止用户名等内容注入邮件头。
func sanitizeHeader(value string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
}
