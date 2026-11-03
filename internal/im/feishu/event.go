package feishu

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// 本文件实现飞书事件推送的**签名校验与解密**（纯 stdlib，零 cgo）。
//
// 为什么在长轮询兜底之外还要有它：桌面应用在 NAT 后面收不到 webhook（design
// §4.8），所以本驱动当前用 im/v1/messages 历史接口做长轮询，这条路径不需要
// 验签/解密。但"事件订阅"（webhook 或未来接入的长连接 SDK）是飞书的首选接入，
// 任何要处理的原始事件都必须先过这两步才能被信任。把协议算法放在这里，接线方
// 只调用两个函数，不必各自复刻飞书的 CBC/PKCS7 细节（也避免出现"解密写错但
// 没人发现"的静默失败）。
//
// 协议（飞书开放平台"事件订阅-加密"）：
//   - 签名：hex(sha256(timestamp + nonce + encryptKey + rawBody))
//   - 密文：AES-256-CBC，key = sha256(encryptKey)，IV 为密文前 16 字节，
//     PKCS#7 填充；密文以 base64 放在信封 {"encrypt":"..."} 里。
//
// 这两个函数只做协议算法；事件路由（challenge 应答、事件类型分派）属于接线层。

// maxEventBodyBytes 是解密前允许的最大信封大小：事件体远小于它，限制可以避
// 免一条坏请求把内存撑爆。
const maxEventBodyBytes = 4 << 20 // 4 MiB

// EventSignature 计算飞书事件签名（hex）。timestamp/nonce 来自请求头
// X-Lark-Request-Timestamp / X-Lark-Request-Nonce，body 是原始请求体（解密前）。
func EventSignature(timestamp, nonce, encryptKey string, body []byte) string {
	h := sha256.New()
	_, _ = h.Write([]byte(timestamp))
	_, _ = h.Write([]byte(nonce))
	_, _ = h.Write([]byte(encryptKey))
	_, _ = h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// CheckEventSignature 用常量时间比较校验签名。encryptKey 为空表示未启用加密，
// 此时飞书不发送签名头，调用方应按"明文 + verification token"处理，不要调用
// 本函数（返回 error 让误用可见）。
func CheckEventSignature(timestamp, nonce, encryptKey string, body []byte, got string) error {
	if encryptKey == "" {
		return errors.New("feishu: event signature needs an encrypt key")
	}
	want := EventSignature(timestamp, nonce, encryptKey, body)
	if subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
		return errors.New("feishu: event signature mismatch")
	}
	return nil
}

// CheckVerificationToken 校验明文事件的 token（v1 顶层 token / v2 header.token）。
// 用常量时间比较，避免逐字节比较带来的时序差异。
func CheckVerificationToken(want, got string) error {
	if want == "" {
		return errors.New("feishu: verification token is not configured")
	}
	if subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
		return errors.New("feishu: event verification token mismatch")
	}
	return nil
}

// eventEnvelope 是加密事件的信封：只有一个 base64 密文字段。
type eventEnvelope struct {
	Encrypt string `json:"encrypt"`
}

// DecryptEvent 解开一个加密事件的信封，返回明文 JSON。
//
// 只做解密，不做签名校验：签名要覆盖**解密前**的请求体与请求头，必须在解密
// 之前单独调用 CheckEventSignature（顺序不能反）。
func DecryptEvent(encryptKey string, body []byte) ([]byte, error) {
	if encryptKey == "" {
		return nil, errors.New("feishu: event decryption needs an encrypt key")
	}
	if len(body) == 0 || len(body) > maxEventBodyBytes {
		return nil, fmt.Errorf("feishu: event body size %d out of range", len(body))
	}
	var env eventEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("feishu: bad event envelope: %w", err)
	}
	if env.Encrypt == "" {
		return nil, errors.New("feishu: event envelope has no ciphertext")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(env.Encrypt)
	if err != nil {
		return nil, fmt.Errorf("feishu: bad event ciphertext: %w", err)
	}
	return decryptCBC(encryptKey, ciphertext)
}

// decryptCBC 是飞书事件加密的解密实现：AES-256-CBC + PKCS#7。
func decryptCBC(encryptKey string, ciphertext []byte) ([]byte, error) {
	key := sha256.Sum256([]byte(encryptKey))
	if len(ciphertext) <= aes.BlockSize || len(ciphertext)%aes.BlockSize != 0 {
		return nil, errors.New("feishu: event ciphertext is not block aligned")
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("feishu: event cipher init: %w", err)
	}
	iv := ciphertext[:aes.BlockSize]
	plain := make([]byte, len(ciphertext)-aes.BlockSize)
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ciphertext[aes.BlockSize:])
	return unpadPKCS7(plain)
}

// unpadPKCS7 去掉 PKCS#7 填充，并拒绝非法填充（错误密钥/被篡改的密文几乎总
// 会在这里暴露，而不是把垃圾 JSON 交给上层）。
func unpadPKCS7(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("feishu: empty event plaintext")
	}
	pad := int(data[len(data)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(data) {
		return nil, errors.New("feishu: bad event padding")
	}
	for _, b := range data[len(data)-pad:] {
		if int(b) != pad {
			return nil, errors.New("feishu: bad event padding")
		}
	}
	return data[:len(data)-pad], nil
}
