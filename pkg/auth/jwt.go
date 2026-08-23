package auth

import (
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// JWTManager 管理 JWT 的生成与解析
type JWTManager struct {
	secret     []byte
	expireTime time.Duration
	issuer     string
}

// NewJWTManager 创建 JWT 管理器
func NewJWTManager(secret string, expireTime time.Duration, issuer string) *JWTManager {
	if issuer == "" {
		issuer = "myproject"
	}
	return &JWTManager{
		secret:     []byte(secret),
		expireTime: expireTime,
		issuer:     issuer,
	}
}

// Claims 自定义 JWT Claims
type Claims struct {
	UserID   uint64 `json:"user_id"`
	Username string `json:"username"`
	jwt.RegisteredClaims
}

// GenerateToken 生成 JWT Token
func (m *JWTManager) GenerateToken(userID uint64, username string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:   userID,
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(m.expireTime)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    m.issuer,
			Subject:   strconv.FormatUint(userID, 10),
		},
	}

	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
}

// ParseToken 解析并校验 JWT Token。
//
// 显式限定签名算法与签发者：不限定算法时存在算法混淆攻击面
// （攻击者把 alg 改成 none 或非对称算法）。jwt/v5 有默认保护，
// 但显式声明是零成本的稳妥做法。
func (m *JWTManager) ParseToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(
		tokenString,
		&Claims{},
		func(token *jwt.Token) (interface{}, error) {
			return m.secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(m.issuer),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, jwt.ErrTokenInvalidClaims
	}
	return claims, nil
}

// ExpireDuration 返回 token 有效期，便于登录接口返回 expires_in
func (m *JWTManager) ExpireDuration() time.Duration {
	return m.expireTime
}
