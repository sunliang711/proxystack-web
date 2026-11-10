package notify

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestMessageWithAttachment 验证带附件的邮件是合法的 multipart/mixed：正文和附件都能按 MIME 解析回原文。
func TestMessageWithAttachment(t *testing.T) {
	data := bytes.Repeat([]byte("secrets: encrypted\n"), 20)
	raw := Message("panel@example.com", []string{"ops@example.com"}, "配置导出", "正文", time.Unix(0, 0),
		Attachment{Name: "proxystack-all-20261110-043000.yaml", ContentType: "application/yaml", Data: data})
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	require.NoError(t, err)
	mediaType, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
	require.NoError(t, err)
	require.Equal(t, "multipart/mixed", mediaType)
	reader := multipart.NewReader(message.Body, params["boundary"])

	part, err := reader.NextPart()
	require.NoError(t, err)
	require.Equal(t, "text/plain; charset=utf-8", part.Header.Get("Content-Type"))
	require.Equal(t, "正文", string(decodePart(t, part)))

	part, err = reader.NextPart()
	require.NoError(t, err)
	require.Equal(t, "proxystack-all-20261110-043000.yaml", part.FileName())
	require.Equal(t, "base64", part.Header.Get("Content-Transfer-Encoding"))
	require.Equal(t, data, decodePart(t, part))

	_, err = reader.NextPart()
	require.ErrorIs(t, err, io.EOF)
}

func decodePart(t *testing.T, part *multipart.Part) []byte {
	t.Helper()
	content, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, part))
	require.NoError(t, err)
	return content
}

// TestMessagePlainText 验证没有附件时仍是单段纯文本。
func TestMessagePlainText(t *testing.T) {
	raw := Message("panel@example.com", []string{"ops@example.com"}, "测试", "正文", time.Unix(0, 0))
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	require.NoError(t, err)
	require.Equal(t, "text/plain; charset=utf-8", message.Header.Get("Content-Type"))
}
