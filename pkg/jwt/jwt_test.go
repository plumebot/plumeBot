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
	var sb strings.Builder
	sb.WriteString(token)
	b := []byte(sb.String())
	last := len(b) - 1
	if b[last] == 'a' {
		b[last] = 'b'
	} else {
		b[last] = 'a'
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