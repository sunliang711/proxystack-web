package subscription

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// 订阅包文件的标识。
const (
	BundleFormat  = "proxystack-web.sub-bundle"
	BundleVersion = 1
	// MaxBundleSize 是订阅包的大小上限。
	MaxBundleSize = 64 << 20
)

const keyPrefix = "ed25519:"

// bundleFile 是订阅包的文件结构。header 字段只用于在验签前找到来源对应的公钥，
// 验签通过后以 payload 中的内容为准，并要求与 header 一致。
type bundleFile struct {
	Format      string    `json:"format"`
	Version     int       `json:"version"`
	Source      string    `json:"source"`
	Sequence    int64     `json:"sequence"`
	GeneratedAt time.Time `json:"generated_at"`
	PublicKey   string    `json:"public_key"`
	Payload     []byte    `json:"payload"`
	Signature   []byte    `json:"signature"`
}

// FormatPublicKey 把公钥编码为 ed25519:<base64>。
func FormatPublicKey(key ed25519.PublicKey) string {
	return keyPrefix + base64.StdEncoding.EncodeToString(key)
}

// ParsePublicKey 解析 ed25519:<base64> 格式的公钥。
func ParsePublicKey(text string) (ed25519.PublicKey, error) {
	encoded, ok := strings.CutPrefix(strings.TrimSpace(text), keyPrefix)
	if !ok {
		return nil, fmt.Errorf("public key must start with %q", keyPrefix)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("public key is not a valid ed25519 key")
	}
	return ed25519.PublicKey(raw), nil
}

// KeyFingerprint 返回公钥 sha256 的前 8 字节十六进制，用于界面和日志中核对公钥。
func KeyFingerprint(key ed25519.PublicKey) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:8])
}

// Seal 用私钥签名快照，生成订阅包文件内容。
func Seal(snapshot *Snapshot, key ed25519.PrivateKey) ([]byte, error) {
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	file := bundleFile{
		Format: BundleFormat, Version: BundleVersion,
		Source: snapshot.Source, Sequence: snapshot.Sequence, GeneratedAt: snapshot.GeneratedAt.UTC(),
		PublicKey: FormatPublicKey(key.Public().(ed25519.PublicKey)),
		Payload:   payload, Signature: ed25519.Sign(key, payload),
	}
	return json.MarshalIndent(file, "", "  ")
}

// Header 是订阅包的未验签头部信息。
type Header struct {
	Source      string
	Sequence    int64
	GeneratedAt time.Time
	PublicKey   string
}

// ReadHeader 读取订阅包头部，不验签，用于在验签前找到来源。
func ReadHeader(data []byte) (Header, error) {
	file, err := decodeFile(data)
	if err != nil {
		return Header{}, err
	}
	return Header{Source: file.Source, Sequence: file.Sequence, GeneratedAt: file.GeneratedAt, PublicKey: file.PublicKey}, nil
}

func decodeFile(data []byte) (*bundleFile, error) {
	if len(data) > MaxBundleSize {
		return nil, errors.New("subscription bundle is too large")
	}
	var file bundleFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return nil, fmt.Errorf("not a subscription bundle: %w", err)
	}
	if file.Format != BundleFormat {
		return nil, errors.New("not a proxystack-web subscription bundle")
	}
	if file.Version != BundleVersion {
		return nil, fmt.Errorf("unsupported subscription bundle version %d", file.Version)
	}
	return &file, nil
}

// ErrBadSignature 表示订阅包签名与该来源的公钥不符：包被修改过，或不是该来源签发的。
var ErrBadSignature = errors.New("signature does not match the source's public key")

// Open 用 trusted 返回的来源公钥验签，返回包内的快照。
func Open(data []byte, trusted func(source string) (ed25519.PublicKey, error)) (*Snapshot, error) {
	file, err := decodeFile(data)
	if err != nil {
		return nil, err
	}
	key, err := trusted(file.Source)
	if err != nil {
		return nil, err
	}
	if !ed25519.Verify(key, file.Payload, file.Signature) {
		return nil, ErrBadSignature
	}
	var snapshot Snapshot
	decoder := json.NewDecoder(bytes.NewReader(file.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return nil, fmt.Errorf("invalid bundle payload: %w", err)
	}
	if snapshot.Source != file.Source || snapshot.Sequence != file.Sequence {
		return nil, errors.New("bundle header does not match its signed content")
	}
	if err := snapshot.Validate(); err != nil {
		return nil, fmt.Errorf("invalid bundle content: %w", err)
	}
	return &snapshot, nil
}

// ContentHash 返回快照内容的摘要（不含序号和生成时间），用于判断是否有未发布的变更。
func ContentHash(snapshot *Snapshot) (string, error) {
	copy := *snapshot
	copy.Sequence, copy.GeneratedAt = 0, time.Time{}
	data, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
