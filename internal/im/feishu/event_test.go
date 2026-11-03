package feishu

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// encryptEventForTest 是测试侧的事件加密：与飞书服务端的算法对称，用来验证
// DecryptEvent 的协议实现（不引入测试专用的生产 API）。
func encryptEventForTest(t *testing.T, encryptKey string, plain []byte) []byte {
	t.Helper()
	key := sha256.Sum256([]byte(encryptKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	padded := make([]byte, len(plain)+pad)
	copy(padded, plain)
	for i := len(plain); i < len(padded); i++ {
		padded[i] = byte(pad)
	}
	ciphertext := make([]byte, aes.BlockSize+len(padded))
	iv := ciphertext[:aes.BlockSize]
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		t.Fatal(err)
	}
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext[aes.BlockSize:], padded)
	envelope, err := json.Marshal(map[string]string{"encrypt": base64.StdEncoding.EncodeToString(ciphertext)})
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func TestDecryptEventRoundTrip(t *testing.T) {
	const key = "event-encrypt-key"
	plain := []byte(`{"schema":"2.0","header":{"event_type":"im.message.receive_v1"},"event":{"message":{}}}`)
	envelope := encryptEventForTest(t, key, plain)
	got, err := DecryptEvent(key, envelope)
	if err != nil {
		t.Fatalf("DecryptEvent: %v", err)
	}
	if string(got) != string(plain) {
		t.Fatalf("plaintext = %s", got)
	}
	// 明文可以被当作普通事件 JSON 继续解析。
	var parsed struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(got, &parsed); err != nil || parsed.Schema != "2.0" {
		t.Fatalf("decrypted event = %s, %v", got, err)
	}
}

// 错误密钥必须报错，而不是返回一段乱码 JSON 让上层继续处理。
func TestDecryptEventRejectsWrongKey(t *testing.T) {
	envelope := encryptEventForTest(t, "right-key", []byte(`{"ok":true}`))
	if _, err := DecryptEvent("wrong-key", envelope); err == nil {
		t.Fatal("expected an error for the wrong encrypt key")
	}
}

func TestDecryptEventRejectsMalformedInput(t *testing.T) {
	const key = "k"
	cases := map[string][]byte{
		"not json":       []byte("not json"),
		"empty envelope": []byte(`{}`),
		"bad base64":     []byte(`{"encrypt":"%%%"}`),
		"short cipher":   []byte(`{"encrypt":"` + base64.StdEncoding.EncodeToString([]byte("short")) + `"}`),
	}
	for name, body := range cases {
		if _, err := DecryptEvent(key, body); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := DecryptEvent("", []byte(`{"encrypt":"AAAA"}`)); err == nil {
		t.Error("empty key must be rejected")
	}
	if _, err := DecryptEvent(key, nil); err == nil {
		t.Error("empty body must be rejected")
	}
	// 超过信封上限的请求体在解密前就被拒绝。
	big := make([]byte, maxEventBodyBytes+1)
	if _, err := DecryptEvent(key, big); err == nil {
		t.Error("oversized body must be rejected")
	}
}

// 签名算法与飞书文档一致：hex(sha256(timestamp+nonce+encryptKey+rawBody))。
func TestEventSignatureVerification(t *testing.T) {
	const (
		timestamp  = "1700000000"
		nonce      = "nonce-1"
		encryptKey = "encrypt-key"
	)
	body := []byte(`{"encrypt":"abc"}`)
	want := EventSignature(timestamp, nonce, encryptKey, body)
	if len(want) != 64 {
		t.Fatalf("signature = %q, want 64 hex chars", want)
	}
	if err := CheckEventSignature(timestamp, nonce, encryptKey, body, want); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	// 常量时间比较：任一输入变化都必须失败。
	bad := map[string]func() error{
		"tampered body": func() error {
			return CheckEventSignature(timestamp, nonce, encryptKey, []byte(`{"encrypt":"abd"}`), want)
		},
		"wrong timestamp": func() error {
			return CheckEventSignature("1700000001", nonce, encryptKey, body, want)
		},
		"wrong nonce": func() error {
			return CheckEventSignature(timestamp, "nonce-2", encryptKey, body, want)
		},
		"wrong key": func() error {
			return CheckEventSignature(timestamp, nonce, "other-key", body, want)
		},
	}
	for name, check := range bad {
		if err := check(); err == nil {
			t.Errorf("%s: expected a signature mismatch", name)
		}
	}
	if err := CheckEventSignature(timestamp, nonce, "", body, want); err == nil {
		t.Fatal("empty encrypt key must be rejected")
	}
}

func TestCheckVerificationToken(t *testing.T) {
	if err := CheckVerificationToken("token", "token"); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	if err := CheckVerificationToken("token", "other"); err == nil {
		t.Fatal("mismatched token must be rejected")
	}
	if err := CheckVerificationToken("", ""); err == nil {
		t.Fatal("empty configured token must be rejected")
	}
}

// 加密事件里的消息体与明文事件走同一套 mapping：解密后能直接翻译成 RawEvent。
func TestDecryptedEventTranslatesLikePlaintext(t *testing.T) {
	const key = "k"
	plain := []byte(`{"schema":"2.0","header":{"event_type":"im.message.receive_v1","token":"t"},` +
		`"event":{"sender":{"sender_id":{"open_id":"ou_alice"},"sender_type":"user"},` +
		`"message":{"message_id":"om_1","chat_id":"oc_1","chat_type":"p2p","msg_type":"text",` +
		`"create_time":"1700000000000","content":"{\"text\":\"hi\"}"}}}`)
	decrypted, err := DecryptEvent(key, encryptEventForTest(t, key, plain))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(decrypted), "im.message.receive_v1") {
		t.Fatalf("decrypted = %s", decrypted)
	}
}
