package auth

import (
	"errors"

	"golang.org/x/crypto/bcrypt"
)

// MaxPasswordBytes bcrypt 的硬上限。
//
// 注意是「字节」不是「字符」：validator 的 max= 按 rune 计数，
// 25 个中文字符就是 75 字节，已经超过这个上限。
const MaxPasswordBytes = 72

// ErrPasswordTooLong 密码超过 bcrypt 上限。
// 单独暴露一个哨兵错误，让上层把它映射成 400 而不是 500 ——
// 这是输入问题，不是内部故障。
var ErrPasswordTooLong = errors.New("auth: 密码长度超过 72 字节")

// HashPassword 对密码进行哈希处理
func HashPassword(password string) (string, error) {
	if len(password) > MaxPasswordBytes {
		return "", ErrPasswordTooLong
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// VerifyPassword 验证密码是否匹配
func VerifyPassword(hashedPassword, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
	return err == nil
}
