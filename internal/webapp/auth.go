package webapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

var errBadInitData = errors.New("invalid init data")

// validateInitData checks Telegram Mini App init data and returns the Telegram user id.
// See https://core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app
func validateInitData(raw, botToken string, maxAge time.Duration, now time.Time) (int64, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return 0, errBadInitData
	}
	hash := values.Get("hash")
	if hash == "" {
		return 0, errBadInitData
	}

	keys := make([]string, 0, len(values))
	for k := range values {
		if k != "hash" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	pairs := make([]string, len(keys))
	for i, k := range keys {
		pairs[i] = k + "=" + values.Get(k)
	}

	secret := hmacSHA256([]byte("WebAppData"), []byte(botToken))
	expected := hex.EncodeToString(hmacSHA256(secret, []byte(strings.Join(pairs, "\n"))))
	if !hmac.Equal([]byte(expected), []byte(hash)) {
		return 0, errBadInitData
	}

	authDate, err := strconv.ParseInt(values.Get("auth_date"), 10, 64)
	if err != nil || now.Sub(time.Unix(authDate, 0)) > maxAge {
		return 0, errBadInitData
	}

	var user struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(values.Get("user")), &user); err != nil || user.ID == 0 {
		return 0, errBadInitData
	}
	return user.ID, nil
}

func hmacSHA256(key, msg []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return m.Sum(nil)
}
