package proto

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

// Shared helpers for authentication and pairing. The Android app
// implements exactly the same functions (see Crypto.kt).

// AuthMAC is the proof a paired client sends in response to a challenge.
func AuthMAC(secret []byte, nonce, serverID, clientID string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte("syncme-auth-v1\n" + nonce + "\n" + serverID + "\n" + clientID))
	return hex.EncodeToString(m.Sum(nil))
}

// VerifyMAC compares MACs in constant time.
func VerifyMAC(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// PairCode is the 6 digit verification code both screens show while
// pairing. It binds the server key, the client id and the one-time token,
// so a man-in-the-middle produces different codes on the two devices.
func PairCode(serverID, clientID, token string) string {
	sum := sha256.Sum256([]byte("syncme-pair-v1\n" + serverID + "\n" + clientID + "\n" + strings.ToUpper(token)))
	v := binary.BigEndian.Uint32(sum[:4]) % 1000000
	return fmt.Sprintf("%06d", v)
}

const tokenAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// NewToken returns an 8 character one-time pairing token (40 bits).
func NewToken() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	out := make([]byte, 8)
	for i := range b {
		out[i] = tokenAlphabet[int(b[i])%len(tokenAlphabet)]
	}
	return string(out)
}

// RandomHex returns n random bytes hex encoded.
func RandomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// RelayListenMessage is what a server signs to claim its id on a relay.
func RelayListenMessage(nonce string) []byte {
	return []byte("syncme-relay-listen-v1:" + nonce)
}
