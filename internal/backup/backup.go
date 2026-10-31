// Package backup 读写迁移包：tar.gz 归档，默认用 age 按口令（scrypt）整体加密。
package backup

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"
)

const (
	// Extension 是迁移包的文件扩展名。
	Extension = ".pswbak"
	// FormatVersion 是迁移包格式版本。
	FormatVersion = 1

	manifestName = "manifest.json"
	// DatabaseName 是包内数据库快照的文件名。
	DatabaseName = "proxystack.db"
	// TLSCertName、TLSKeyName 是包内面板证书和私钥的文件名。
	TLSCertName = "tls/cert.pem"
	TLSKeyName  = "tls/key.pem"

	ageHeader = "age-encryption.org/v1"
)

// Manifest 描述迁移包的内容。
type Manifest struct {
	FormatVersion int               `json:"format_version"`
	AppVersion    string            `json:"app_version"`
	SchemaVersion int64             `json:"schema_version"`
	ExportedAt    time.Time         `json:"exported_at"`
	Hostname      string            `json:"hostname"`
	ExternalHost  string            `json:"external_host"`
	Files         map[string]string `json:"files"`
	Counts        map[string]int64  `json:"counts"`
	// TrafficFrom、TrafficTo 是流量明细的时间范围，没有流量时为零值。
	TrafficFrom time.Time `json:"traffic_from"`
	TrafficTo   time.Time `json:"traffic_to"`
	// Deps 是源实例各依赖组件的版本。
	Deps map[string]string `json:"deps"`
}

// File 是要打包的文件：Name 是包内路径，Path 是本地路径。
type File struct {
	Name string
	Path string
}

// ErrPassphraseRequired 表示迁移包已加密但没有提供口令。
var ErrPassphraseRequired = errors.New("the package is encrypted; a passphrase is required")

// ErrWrongPassphrase 表示口令不正确。
var ErrWrongPassphrase = errors.New("wrong passphrase")

func hashFile(name string) (string, error) {
	file, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer file.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// Write 把文件打包写入 w。passphrase 为空时不加密。manifest 的 Files 由这里填写。
func Write(w io.Writer, manifest Manifest, files []File, passphrase string) error {
	manifest.FormatVersion = FormatVersion
	manifest.Files = map[string]string{}
	for _, file := range files {
		digest, err := hashFile(file.Path)
		if err != nil {
			return err
		}
		manifest.Files[file.Name] = digest
	}
	out := w
	var encrypted io.WriteCloser
	if passphrase != "" {
		recipient, err := age.NewScryptRecipient(passphrase)
		if err != nil {
			return err
		}
		if encrypted, err = age.Encrypt(w, recipient); err != nil {
			return err
		}
		out = encrypted
	}
	compressed := gzip.NewWriter(out)
	archive := tar.NewWriter(compressed)
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := archive.WriteHeader(&tar.Header{Name: manifestName, Mode: 0o640, Size: int64(len(data)), ModTime: manifest.ExportedAt}); err != nil {
		return err
	}
	if _, err := archive.Write(data); err != nil {
		return err
	}
	for _, file := range files {
		if err := addFile(archive, file, manifest.ExportedAt); err != nil {
			return err
		}
	}
	if err := archive.Close(); err != nil {
		return err
	}
	if err := compressed.Close(); err != nil {
		return err
	}
	if encrypted != nil {
		return encrypted.Close()
	}
	return nil
}

func addFile(archive *tar.Writer, file File, at time.Time) error {
	source, err := os.Open(file.Path)
	if err != nil {
		return err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return err
	}
	if err := archive.WriteHeader(&tar.Header{Name: file.Name, Mode: 0o640, Size: info.Size(), ModTime: at}); err != nil {
		return err
	}
	_, err = io.Copy(archive, source)
	return err
}

// Read 解开迁移包到 dest，校验 manifest 和各文件 sha256 后返回 manifest。只解出 manifest 中列出的文件。
func Read(r io.Reader, passphrase, dest string) (*Manifest, error) {
	buffered := bufio.NewReader(r)
	head, err := buffered.Peek(len(ageHeader))
	if err != nil {
		return nil, errors.New("not a proxystack-web migration package")
	}
	var plain io.Reader = buffered
	if bytes.Equal(head, []byte(ageHeader)) {
		if passphrase == "" {
			return nil, ErrPassphraseRequired
		}
		identity, err := age.NewScryptIdentity(passphrase)
		if err != nil {
			return nil, err
		}
		if plain, err = age.Decrypt(buffered, identity); err != nil {
			var noMatch *age.NoIdentityMatchError
			if errors.As(err, &noMatch) {
				return nil, ErrWrongPassphrase
			}
			return nil, fmt.Errorf("decrypt package: %w", err)
		}
	}
	compressed, err := gzip.NewReader(plain)
	if err != nil {
		return nil, errors.New("not a proxystack-web migration package")
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)

	header, err := archive.Next()
	if err != nil || header.Name != manifestName {
		return nil, errors.New("not a proxystack-web migration package: manifest is missing")
	}
	var manifest Manifest
	if err := json.NewDecoder(archive).Decode(&manifest); err != nil {
		return nil, fmt.Errorf("invalid manifest: %w", err)
	}
	if manifest.FormatVersion != FormatVersion {
		return nil, fmt.Errorf("unsupported package format version %d", manifest.FormatVersion)
	}
	seen := map[string]bool{}
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read package: %w", err)
		}
		want, ok := manifest.Files[header.Name]
		if !ok || header.Typeflag != tar.TypeReg || !safeName(header.Name) {
			return nil, fmt.Errorf("unexpected file in package: %s", header.Name)
		}
		if err := extract(archive, filepath.Join(dest, filepath.FromSlash(header.Name)), want); err != nil {
			return nil, fmt.Errorf("%s: %w", header.Name, err)
		}
		seen[header.Name] = true
	}
	for name := range manifest.Files {
		if !seen[name] {
			return nil, fmt.Errorf("file listed in the manifest is missing: %s", name)
		}
	}
	if !seen[DatabaseName] {
		return nil, errors.New("the package has no database")
	}
	return &manifest, nil
}

// safeName 只接受包内的相对路径，防止路径穿越。
func safeName(name string) bool {
	return name != "" && !path.IsAbs(name) && path.Clean(name) == name && !strings.HasPrefix(name, "../") && name != ".."
}

func extract(source io.Reader, target, digest string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	sum := sha256.New()
	_, err = io.Copy(io.MultiWriter(file, sum), source)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if hex.EncodeToString(sum.Sum(nil)) != digest {
		return errors.New("sha256 mismatch")
	}
	return nil
}
