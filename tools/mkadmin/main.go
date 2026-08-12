package main

import (
	"database/sql"
	"fmt"
	"os"

	_ "github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	dsn := os.Getenv("SUB2API_ADMIN_DSN")
	email := os.Getenv("SUB2API_ADMIN_EMAIL")
	pass := os.Getenv("SUB2API_ADMIN_PASSWORD")
	if dsn == "" || email == "" || pass == "" {
		fmt.Fprintln(os.Stderr, "SUB2API_ADMIN_DSN, SUB2API_ADMIN_EMAIL and SUB2API_ADMIN_PASSWORD are required")
		os.Exit(2)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		panic(err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		panic(err)
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE email=$1`, email).Scan(&n); err != nil {
		panic(err)
	}
	if n > 0 {
		_, err = db.Exec(`UPDATE users SET password_hash=$1, role='admin', status='active', updated_at=NOW() WHERE email=$2`, string(hash), email)
	} else {
		_, err = db.Exec(`INSERT INTO users (email, password_hash, role, balance, concurrency, status, created_at, updated_at)
			VALUES ($1, $2, 'admin', 0, 50, 'active', NOW(), NOW())`, email, string(hash))
	}
	if err != nil {
		panic(err)
	}
	fmt.Printf("administrator ready: %s\n", email)
}
