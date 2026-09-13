package jwt

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSignVerifyRoundtrip(t *testing.T) {
	m := NewManager("test-secret", time.Hour)
	token, expiresAt, err := m.Sign("admin")
	if err != nil {
		t.Fatalf("Sign 失败: %v", err)
	}
	if token == "" {
		t.Fatal("token 不应为空")
	}
	if expiresAt <= time.Now().Unix() {
		t.Fatalf("expiresAt 应在未来, 实际: %d", expiresAt)
	}

	claims, err := m.Verify(token)
	if err != nil {
		t.Fatalf("Verify 失败: %v", err)
	}
	if claims.Username != "admin" {
		t.Fatalf("Username 应还原, 实际: %q", claims.Username)
	}
}

func TestVerifyTamperedToken(t *testing.T) {
	m := NewManager("test-secret", time.Hour)
	token, _, err := m.Sign("admin")
	if err != nil {
		t.Fatalf("Sign 失败: %v", err)
	}
	// 篡改载荷段最后一个 base64 字符。
	// 注意不能只改签名段末位：base64url 末位字符只有高 4~6 位是有效数据位，低位是
	// 补零 bit，改它们不改变解码结果，验签照样通过（实测约 5.6% 概率，原实现因此间歇性假失败）。
	// 改载荷段则任何解码差异都会让 HMAC 不匹配，判定稳定。
	var sb strings.Builder
	sb.WriteString(token)
	b := []byte(sb.String())
	dot := strings.LastIndexByte(token, '.')
	payloadLast := dot - 1
	if b[payloadLast] == 'a' {
		b[payloadLast] = 'b'
	} else {
		b[payloadLast] = 'a'
	}
	if _, err := m.Verify(string(b)); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("篡改 token 应拒验, 实际: %v", err)
	}
}

func TestVerifyExpiredToken(t *testing.T) {
	// ttl 为负 → 签出的 token 立即过期。
	m := NewManager("test-secret", -time.Second)
	token, _, err := m.Sign("admin")
	if err != nil {
		t.Fatalf("Sign 失败: %v", err)
	}
	if _, err := m.Verify(token); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("过期 token 应拒验, 实际: %v", err)
	}
}

func TestVerifyMalformedToken(t *testing.T) {
	m := NewManager("test-secret", time.Hour)
	for _, bad := range []string{"", "not.a.token", "aaa.bbb.ccc"} {
		if _, err := m.Verify(bad); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("畸形 token %q 应拒验, 实际: %v", bad, err)
		}
	}
}

func TestVerifyWrongSecret(t *testing.T) {
	sign := NewManager("secret-a", time.Hour)
	check := NewManager("secret-b", time.Hour)
	token, _, err := sign.Sign("admin")
	if err != nil {
		t.Fatalf("Sign 失败: %v", err)
	}
	if _, err := check.Verify(token); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("密钥不符应拒验, 实际: %v", err)
	}
}

func TestSignTTLMatchesExpiresAt(t *testing.T) {
	m := NewManager("test-secret", 90*time.Minute)
	token, expiresAt, err := m.Sign("admin")
	if err != nil {
		t.Fatalf("Sign 失败: %v", err)
	}
	claims, err := m.Verify(token)
	if err != nil {
		t.Fatalf("Verify 失败: %v", err)
	}
	if claims.ExpiresAt == nil || claims.ExpiresAt.Unix() != expiresAt {
		t.Fatalf("claims.exp 应等于 Sign 返回的 expiresAt, 实际: %v vs %d", claims.ExpiresAt, expiresAt)
	}
}