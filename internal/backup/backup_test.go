package backup_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/eagle/proxystack-web/internal/backup"
)

func sample(t *testing.T) []backup.File {
	t.Helper()
	dir := t.TempDir()
	db := filepath.Join(dir, "db")
	cert := filepath.Join(dir, "cert")
	require.NoError(t, os.WriteFile(db, []byte("database"), 0o640))
	require.NoError(t, os.WriteFile(cert, []byte("certificate"), 0o640))
	return []backup.File{{Name: backup.DatabaseName, Path: db}, {Name: backup.TLSCertName, Path: cert}}
}

// TestRoundTrip 验证加密和不加密的迁移包都能完整读回，口令错误和缺失时给出明确错误。
func TestRoundTrip(t *testing.T) {
	manifest := backup.Manifest{AppVersion: "v1", SchemaVersion: 6, ExportedAt: time.Unix(1_800_000_000, 0).UTC(), Hostname: "old"}
	for _, passphrase := range []string{"", "correct horse"} {
		var data bytes.Buffer
		require.NoError(t, backup.Write(&data, manifest, sample(t), passphrase))
		dest := t.TempDir()
		got, err := backup.Read(bytes.NewReader(data.Bytes()), passphrase, dest)
		require.NoError(t, err)
		require.Equal(t, "old", got.Hostname)
		require.Len(t, got.Files, 2)
		content, err := os.ReadFile(filepath.Join(dest, "tls", "cert.pem"))
		require.NoError(t, err)
		require.Equal(t, "certificate", string(content))
		if passphrase != "" {
			require.NotContains(t, data.String(), "database")
			_, err = backup.Read(bytes.NewReader(data.Bytes()), "", t.TempDir())
			require.ErrorIs(t, err, backup.ErrPassphraseRequired)
			_, err = backup.Read(bytes.NewReader(data.Bytes()), "wrong", t.TempDir())
			require.ErrorIs(t, err, backup.ErrWrongPassphrase)
		}
	}
}

// TestRejectsCorruptPackage 验证损坏的文件不被接受。
func TestRejectsCorruptPackage(t *testing.T) {
	_, err := backup.Read(bytes.NewReader([]byte("this is not a package at all, sorry")), "", t.TempDir())
	require.Error(t, err)

	var data bytes.Buffer
	require.NoError(t, backup.Write(&data, backup.Manifest{ExportedAt: time.Unix(1, 0)}, sample(t), ""))
	corrupted := data.Bytes()
	corrupted[len(corrupted)/2] ^= 0xff
	_, err = backup.Read(bytes.NewReader(corrupted), "", t.TempDir())
	require.Error(t, err)
}
