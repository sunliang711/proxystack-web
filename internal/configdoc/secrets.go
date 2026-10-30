package configdoc

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// 口令加密参数：argon2id 派生 AES-256-GCM 密钥，字段值写成 enc:<base64(nonce|密文)>。
const (
	encPrefix     = "enc:"
	kdfName       = "argon2id"
	kdfTime       = 3
	kdfMemory     = 64 * 1024
	kdfThreads    = 2
	checkSentinel = "proxystack-web"
)

// ErrPassphraseRequired 表示文档已加密但没有提供口令。
var ErrPassphraseRequired = errors.New("the file is encrypted; a passphrase is required")

// ErrWrongPassphrase 表示口令不正确。
var ErrWrongPassphrase = errors.New("wrong passphrase")

// secretField 指向文档中的一个凭据字段。Regenerate 为空表示外部凭据（例如上游代理的密码），
// 无法在本机重新生成，「不包含」模式下照常输出。
type secretField struct {
	Value      *string
	Regenerate func() string
}

// secretFields 列出文档内全部凭据字段。
func secretFields(doc *Document) []secretField {
	fields := []secretField{}
	if doc.Settings != nil {
		fields = append(fields, secretField{&doc.Settings.DownloadProxy.Password, nil})
	}
	for userIndex := range doc.Users {
		user := &doc.Users[userIndex]
		fields = append(fields,
			secretField{&user.UUID, RandomUUID},
			secretField{&user.SSPassword, userShadowsocksKey(doc, user.Name, DefaultProfile)},
			secretField{&user.SubToken, RandomToken},
		)
		for profileIndex := range user.Profiles {
			profile := &user.Profiles[profileIndex]
			fields = append(fields,
				secretField{&profile.UUID, RandomUUID},
				secretField{&profile.SSPassword, userShadowsocksKey(doc, user.Name, profile.Name)},
			)
		}
	}
	for stackIndex := range doc.Stacks {
		stack := &doc.Stacks[stackIndex]
		fields = append(fields,
			secretField{&stack.Mihomo.ControllerSecret, RandomSecret},
			secretField{&stack.Xray.Outbound.Password, nil},
		)
		for inboundIndex := range stack.Xray.Inbounds {
			inbound := &stack.Xray.Inbounds[inboundIndex]
			method := inbound.Method
			fields = append(fields,
				secretField{&inbound.Password, func() string { return RandomShadowsocksKey(method) }},
				secretField{&inbound.AuthPassword, RandomPassword},
			)
		}
		for _, listener := range []*Listener{&stack.Mihomo.Socks, &stack.Mihomo.HTTP} {
			for accountIndex := range listener.Accounts {
				fields = append(fields, secretField{&listener.Accounts[accountIndex].Password, RandomPassword})
			}
		}
	}
	return fields
}

// userShadowsocksKey 返回为用户档案生成 shadowsocks 密码的函数：被 2022 系列入站引用时生成对应长度的密钥。
func userShadowsocksKey(doc *Document, user, profile string) func() string {
	return func() string {
		for _, stack := range doc.Stacks {
			for _, inbound := range stack.Xray.Inbounds {
				if inbound.Protocol != "shadowsocks" {
					continue
				}
				for _, ref := range inbound.Users {
					refProfile := ref.Profile
					if refProfile == "" {
						refProfile = DefaultProfile
					}
					if ref.User == user && refProfile == profile {
						return RandomShadowsocksKey(inbound.Method)
					}
				}
			}
		}
		return RandomPassword()
	}
}

// ApplySecrets 按导出方式处理文档中的凭据。encrypted 模式需要口令。
func ApplySecrets(doc *Document, mode, passphrase string) error {
	doc.Secrets = mode
	doc.Encryption = nil
	switch mode {
	case SecretsPlain:
		return nil
	case SecretsOmitted:
		for _, field := range secretFields(doc) {
			if field.Regenerate != nil && field.Value != nil {
				*field.Value = ""
			}
		}
		return nil
	case SecretsEncrypted:
		if len(passphrase) < 8 {
			return fmt.Errorf("passphrase must be at least 8 characters")
		}
		salt := randomBytes(16)
		key := deriveKey(passphrase, salt, kdfTime, kdfMemory)
		check, err := encryptValue(key, checkSentinel)
		if err != nil {
			return err
		}
		doc.Encryption = &Encryption{
			KDF: kdfName, Salt: base64.StdEncoding.EncodeToString(salt), Time: kdfTime, Memory: kdfMemory, Check: check,
		}
		for _, field := range secretFields(doc) {
			if *field.Value == "" {
				continue
			}
			encrypted, err := encryptValue(key, *field.Value)
			if err != nil {
				return err
			}
			*field.Value = encrypted
		}
		return nil
	default:
		return fmt.Errorf("unknown secrets mode: %s", mode)
	}
}

// Decrypt 解开加密文档中的凭据字段，之后文档的 secrets 视为 plain。
func Decrypt(doc *Document, passphrase string) error {
	if doc.Secrets != SecretsEncrypted {
		return nil
	}
	if doc.Encryption == nil || doc.Encryption.KDF != kdfName {
		return fmt.Errorf("encryption parameters are missing or unsupported")
	}
	if passphrase == "" {
		return ErrPassphraseRequired
	}
	salt, err := base64.StdEncoding.DecodeString(doc.Encryption.Salt)
	if err != nil {
		return fmt.Errorf("encryption salt is invalid")
	}
	key := deriveKey(passphrase, salt, doc.Encryption.Time, doc.Encryption.Memory)
	if check, err := decryptValue(key, doc.Encryption.Check); err != nil || check != checkSentinel {
		return ErrWrongPassphrase
	}
	for _, field := range secretFields(doc) {
		if !strings.HasPrefix(*field.Value, encPrefix) {
			continue
		}
		plain, err := decryptValue(key, *field.Value)
		if err != nil {
			return ErrWrongPassphrase
		}
		*field.Value = plain
	}
	doc.Secrets = SecretsPlain
	doc.Encryption = nil
	return nil
}

// RegenerateMissingSecrets 为空的本机凭据生成新值，返回生成的个数。
func RegenerateMissingSecrets(doc *Document) int {
	count := 0
	for _, field := range secretFields(doc) {
		if field.Regenerate == nil || *field.Value != "" {
			continue
		}
		if !secretNeeded(doc, field.Value) {
			continue
		}
		*field.Value = field.Regenerate()
		count++
	}
	return count
}

// secretNeeded 判断空字段是否需要补齐：用户凭据、controller secret 总是需要，
// 入站密码只在对应协议和认证方式下需要。
func secretNeeded(doc *Document, value *string) bool {
	for stackIndex := range doc.Stacks {
		for inboundIndex := range doc.Stacks[stackIndex].Xray.Inbounds {
			inbound := &doc.Stacks[stackIndex].Xray.Inbounds[inboundIndex]
			if value == &inbound.Password {
				return inbound.Protocol == "shadowsocks"
			}
			if value == &inbound.AuthPassword {
				return inbound.AuthType == "password"
			}
		}
	}
	return true
}

// MaskSecrets 把文档内凭据替换为固定占位，用于差异展示。
func MaskSecrets(doc *Document) {
	for _, field := range secretFields(doc) {
		if *field.Value != "" {
			*field.Value = "******"
		}
	}
}

func deriveKey(passphrase string, salt []byte, time, memory uint32) []byte {
	return argon2.IDKey([]byte(passphrase), salt, time, memory, kdfThreads, 32)
}

func encryptValue(key []byte, plain string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := randomBytes(gcm.NonceSize())
	sealed := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return encPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

func decryptValue(key []byte, value string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, encPrefix))
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	plain, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
