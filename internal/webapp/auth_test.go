package webapp

import (
	"encoding/hex"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testToken = "123456:TEST-token"

// signInitData builds init data the way Telegram does, so tests exercise the real check.
func signInitData(t *testing.T, token string, fields map[string]string) string {
	t.Helper()
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, len(keys))
	for i, k := range keys {
		pairs[i] = k + "=" + fields[k]
	}
	secret := hmacSHA256([]byte("WebAppData"), []byte(token))
	hash := hex.EncodeToString(hmacSHA256(secret, []byte(strings.Join(pairs, "\n"))))

	v := url.Values{}
	for k, val := range fields {
		v.Set(k, val)
	}
	v.Set("hash", hash)
	return v.Encode()
}

func TestValidateInitData(t *testing.T) {
	now := time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC)
	fresh := strconv.FormatInt(now.Add(-time.Hour).Unix(), 10)
	user := `{"id":435209277,"first_name":"Стас"}`

	valid := signInitData(t, testToken, map[string]string{"auth_date": fresh, "user": user, "query_id": "AAH"})
	tampered := strings.Replace(valid, "435209277", "999", 1)

	tests := []struct {
		name    string
		raw     string
		token   string
		wantID  int64
		wantErr bool
	}{
		{name: "valid", raw: valid, token: testToken, wantID: 435209277},
		{name: "signed with another bot token", raw: valid, token: "999:other", wantErr: true},
		{name: "user id changed after signing", raw: tampered, token: testToken, wantErr: true},
		{name: "expired", raw: signInitData(t, testToken, map[string]string{
			"auth_date": strconv.FormatInt(now.Add(-48*time.Hour).Unix(), 10), "user": user,
		}), token: testToken, wantErr: true},
		{name: "no hash", raw: "auth_date=" + fresh + "&user=" + url.QueryEscape(user), token: testToken, wantErr: true},
		{name: "no user", raw: signInitData(t, testToken, map[string]string{"auth_date": fresh}), token: testToken, wantErr: true},
		{name: "empty", raw: "", token: testToken, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := validateInitData(tt.raw, tt.token, 24*time.Hour, now)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got id %d", id)
				}
				return
			}
			if err != nil || id != tt.wantID {
				t.Fatalf("want %d, got %d, %v", tt.wantID, id, err)
			}
		})
	}
}
