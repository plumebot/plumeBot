// Package base64util 提供共享的 base64 解码工具（B-030，统一两处解码策略）。
package base64util

import "encoding/base64"

// Decode 解码 base64 字符串，容忍无填充（RawStdEncoding）变体。
// 先按标准（带 '=' 填充）解码，失败再试无填充，覆盖两种输入形态。
func Decode(s string) ([]byte, error) {
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.RawStdEncoding.DecodeString(s)
}
