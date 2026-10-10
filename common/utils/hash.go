// Package utils 通用工具，与业务无关。
package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Hash 对任意值取 SHA-256 指纹，返回 64 位十六进制串。
//
// 值先经 JSON 编码再取哈希：JSON 是确定性的（结构体按字段声明序、map 按 key 排序、
// 切片保序），于是同一个值在任何进程、任何机器上都得到同一个指纹，可跨次运行直接比较。
//
// 值必须能被 encoding/json 编码。不可编码的类型（channel / func / 含 NaN 的浮点）
// 属于编程错误，直接 panic——比悄悄返回一个"所有失败都长一样"的空指纹好排查。
func Hash(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic("utils.Hash: 值不可 JSON 编码: " + err.Error())
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
