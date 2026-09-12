// Package jwt 封装 golang-jwt/v5（HS256）的签发/验签/解析（P7-001 管理后端鉴权）。
// 纯封装：不依赖 gin、不触碰文件系统——JWT 密钥的生成/持久化是进程装配关注点，由 cmd/bot 侧负责。
package jwt

import (
	"errors"
	"time"

	jwtgo "github.com/golang-jwt/jwt/v5"
)

// ErrInvalidToken 是一切验签失败的收敛错误（篡改/过期/畸形/密钥不符）。
var ErrInvalidToken = errors.New("invalid token")

// Claims 是管理端 JWT 载荷：业务字段 username + 标准注册声明（iat/exp）。
type Claims struct {
	Username string `json:"username"`
	jwtgo.RegisteredClaims
}

// Manager 按注入的密钥与有效期签发/验证 token（HS256）。
type Manager struct {
	secret []byte
	ttl    time.Duration
}

// NewManager 创建 Manager。
func NewManager(secret string, ttl time.Duration) *Manager {
	return &Manager{secret: []byte(secret), ttl: ttl}
}

// Sign 为 username 签发 HS256 token，返回 token 串与过期时间（Unix 秒）。
func (m *Manager) Sign(username string) (token string, expiresAt int64, err error) {
	now := time.Now()
	claims := Claims{
		Username: username,
		RegisteredClaims: jwtgo.RegisteredClaims{
			IssuedAt:  jwtgo.NewNumericDate(now),
			ExpiresAt: jwtgo.NewNumericDate(now.Add(m.ttl)),
		},
	}
	tok := jwtgo.NewWithClaims(jwtgo.SigningMethodHS256, claims)
	signed, err := tok.SignedString(m.secret)
	if err != nil {
		return "", 0, err
	}
	return signed, now.Add(m.ttl).Unix(), nil
}

// Verify 验签并解析 token。任何失败（篡改/过期/畸形/签名算法不符/密钥不符）统一收敛为 ErrInvalidToken。
func (m *Manager) Verify(tokenString string) (*Claims, error) {
	claims := &Claims{}
	tok, err := jwtgo.ParseWithClaims(tokenString, claims,
		func(*jwtgo.Token) (any, error) { return m.secret, nil },
		jwtgo.WithValidMethods([]string{jwtgo.SigningMethodHS256.Alg()}),
		jwtgo.WithExpirationRequired(),
	)
	if err != nil || !tok.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}