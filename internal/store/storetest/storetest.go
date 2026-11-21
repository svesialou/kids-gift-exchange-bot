// Package storetest creates a throwaway MySQL database per test. Tests are skipped without TEST_MYSQL_DSN
// (a server root DSN such as root:secret@tcp(127.0.0.1:3306)/).
package storetest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/svesialou/kids-gift-exchange-bot/internal/store"
)

func New(t *testing.T) *store.Store {
	t.Helper()
	root := os.Getenv("TEST_MYSQL_DSN")
	if root == "" {
		t.Skip("TEST_MYSQL_DSN not set")
	}
	ctx := context.Background()
	admin, err := sql.Open("mysql", root)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("kge_test_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name+" CHARACTER SET utf8mb4"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(ctx, "DROP DATABASE "+name)
		_ = admin.Close()
	})
	st, err := store.Open(ctx, root+name+"?parseTime=true&loc=UTC&multiStatements=true&charset=utf8mb4")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return st
}
