package tgsrv

import (
	"crypto/md5"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

func sha1Hash(p ...string) string {
	hasher := sha1.New()
	for _, s := range p {
		io.WriteString(hasher, s)
	}
	io.WriteString(hasher, QRSalt)
	h := fmt.Sprintf("%x", hasher.Sum(nil))
	return h
}

func md5HashBytes(p ...string) []byte {
	hasher := md5.New()
	for _, s := range p {
		io.WriteString(hasher, s)
	}
	io.WriteString(hasher, QRSalt)
	return hasher.Sum(nil)
}

func md5Hash(p ...string) string {
	return fmt.Sprintf("%x", md5HashBytes(p...))
}

func encodeEmailAndMD5(email string) string {
	email = cleanEmail(email)
	hash64 := base64.StdEncoding.EncodeToString(md5HashBytes(email))
	n := len(hash64) // assert n == 24
	if n != 24 {
		Logger.Errorf("len(base64(md5)) != 24")
	}
	hash64 = strings.ReplaceAll(hash64, "=", "_")
	//hash64 = string([]byte{byte(n)}) + hash64
	s := hash64 + replaceNotBase64(email)
	d := len(s) - 64
	if d <= 0 {
		return s
	}
	return encodeEmailAndMD5(email[:len(email)-d])
}

func cleanEmail(email string) string {
	i := strings.Index(email, "+")
	if i < 0 {
		return email
	}
	j := strings.Index(email, "@")
	if j < 0 {
		return email
	}
	return email[:i] + email[j:]
}

func replaceNotBase64(s string) string {
	s = strings.Replace(s, ".", "_", -1)
	s = strings.Replace(s, "@", "-", -1)
	s = strings.Replace(s, "+", "-", -1)
	return s
}

func decodeEmailAndMD5(p string) (string, error, bool) {
	if len(p) == 0 {
		return "", nil, false
	}
	//n := int(([]byte(p))[0])
	n := 24
	if len(p) <= n+1 {
		return "", nil, false
	}
	hash64 := p[0:n]
	hash64 = strings.ReplaceAll(hash64, "_", "=")
	email := p[n:]
	email = strings.ReplaceAll(email, "_", ".")
	email = strings.ReplaceAll(email, "-", "@")
	i := strings.LastIndex(email, "@")
	if i > 0 {
		email = strings.ReplaceAll(email[:i], "@", "+") + email[i:]
	}
	hash, err := base64.StdEncoding.DecodeString(hash64)
	decodedHex := fmt.Sprintf("%x", hash)
	emailHashHex := fmt.Sprintf("%x", md5HashBytes(email))
	return email, err, emailHashHex == decodedHex
}
