package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP 参数（RFC 6238 默认值，主流验证器应用都支持）。
const (
	totpPeriod = 30
	totpDigits = 6
	// totpSkew 是前后各容许的时间步数，用于吸收手机与服务器之间的时钟偏差。
	totpSkew = 1
)

var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret 生成 160 位随机密钥，返回 Base32 编码（无填充）。
func NewTOTPSecret() string {
	key := make([]byte, 20)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	return totpEncoding.EncodeToString(key)
}

// TOTPStep 返回时间所在的时间步。
func TOTPStep(t time.Time) int64 {
	return t.Unix() / totpPeriod
}

// TOTPCode 计算密钥在指定时间步的验证码。
func TOTPCode(secret string, step int64) (string, error) {
	key, err := totpEncoding.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", fmt.Errorf("invalid TOTP secret: %w", err)
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, value%1_000_000), nil
}

// VerifyTOTP 在当前时间步前后各 totpSkew 步内查找匹配的验证码，返回匹配的时间步。
// 时间步不大于 lastStep 的验证码视为重放，不予接受。
func VerifyTOTP(secret, code string, now time.Time, lastStep int64) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return 0, false
	}
	current := TOTPStep(now)
	for step := current - totpSkew; step <= current+totpSkew; step++ {
		if step <= lastStep {
			continue
		}
		want, err := TOTPCode(secret, step)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

// TOTPURI 返回验证器应用扫码使用的 otpauth 地址。
func TOTPURI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	query := url.Values{}
	query.Set("secret", secret)
	query.Set("issuer", issuer)
	query.Set("algorithm", "SHA1")
	query.Set("digits", fmt.Sprint(totpDigits))
	query.Set("period", fmt.Sprint(totpPeriod))
	return "otpauth://totp/" + label + "?" + query.Encode()
}
