package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"math/big"
	"strconv"
	"strings"
	"time"
)

// AdminSessionTTL is how long an administration session lasts.
const AdminSessionTTL = 12 * time.Hour

// Admin checks the administration password and the sessions it opens.
// Sessions are signed cookies, nothing is kept server side: they survive
// a restart, and changing the password ends them all, since the signing
// key derives from it.
type Admin struct {
	password [sha256.Size]byte // compared hashed: in constant time whatever its length
	key      []byte
	now      func() time.Time
}

// NewAdmin returns the checker of the administration password.
func NewAdmin(password string) *Admin {
	mac := hmac.New(sha256.New, []byte(password))
	mac.Write([]byte("cauri admin session"))
	return &Admin{password: sha256.Sum256([]byte(password)), key: mac.Sum(nil), now: time.Now}
}

const passwordAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// GeneratePassword returns a random password of 64 letters and digits,
// about 381 bits, for when the operator sets none.
func GeneratePassword() string {
	var b strings.Builder
	n := big.NewInt(int64(len(passwordAlphabet)))
	for range 64 {
		i, err := rand.Int(rand.Reader, n) // uniform, unlike a byte modulo 62
		if err != nil {
			panic(err) // crypto/rand does not fail on supported systems
		}
		b.WriteByte(passwordAlphabet[i.Int64()])
	}
	return b.String()
}

// Check reports whether password is the administration password.
func (a *Admin) Check(password string) bool {
	sum := sha256.Sum256([]byte(password))
	return subtle.ConstantTimeCompare(sum[:], a.password[:]) == 1
}

// NewSession returns the value of a session cookie and its expiry.
func (a *Admin) NewSession() (string, time.Time) {
	exp := a.now().Add(AdminSessionTTL).Truncate(time.Second)
	e := strconv.FormatInt(exp.Unix(), 10)
	return e + "." + a.sign(e), exp
}

// ValidSession reports whether value is an unexpired session signed with
// the current password.
func (a *Admin) ValidSession(value string) bool {
	e, sig, ok := strings.Cut(value, ".")
	if !ok {
		return false
	}
	exp, err := strconv.ParseInt(e, 10, 64)
	if err != nil || a.now().Unix() >= exp {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(a.sign(e)))
}

func (a *Admin) sign(expiry string) string {
	mac := hmac.New(sha256.New, a.key)
	mac.Write([]byte(expiry))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
