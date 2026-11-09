package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type SeedUser struct {
	ID    int    `json:"id"`
	Token string `json:"token"`
}

const (
	NumUsers = 50
	Secret   = "HSsuper-secret-ledger-key-123456"
)

func main() {
	users := make([]SeedUser, 0, NumUsers)
	secretKey := []byte(Secret)

	for i := 1; i <= NumUsers; i++ {
		claims := jwt.MapClaims{
			"sub":   fmt.Sprintf("%d", i),
			"admin": true,
			"iat":   time.Now().Unix(),
		}
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		signed, err := token.SignedString(secretKey)
		if err != nil {
			panic(err)
		}

		users = append(users, SeedUser{
			ID:    i,
			Token: "Bearer " + signed,
		})
	}

	jsonFile, err := os.Create("seed_users.json")
	if err != nil {
		panic(fmt.Errorf("failed to create json file: %w", err))
	}
	defer jsonFile.Close()

	bufferedWriter := bufio.NewWriter(jsonFile)
	if err := json.NewEncoder(bufferedWriter).Encode(users); err != nil {
		panic(fmt.Errorf("failed to encode users: %w", err))
	}
	if err := bufferedWriter.Flush(); err != nil {
		panic(fmt.Errorf("failed to flush json buffer: %w", err))
	}

	sqlFile, err := os.Create("scripts/seed.sql")
	if err != nil {
		panic(fmt.Errorf("failed to create sql file: %w", err))
	}
	defer sqlFile.Close()

	sqlTemplate := `
		INSERT INTO accounts (id, balance)
		SELECT generate_series(1, %[1]d), 1000000000000
		ON CONFLICT (id) DO NOTHING;

		-- aligns postgres sequence with manually inserted ids to prevent insert collisions
		SELECT setval('accounts_id_seq', (SELECT MAX(id) FROM accounts));
	`
	if _, err := fmt.Fprintf(sqlFile, sqlTemplate, NumUsers); err != nil {
		panic(fmt.Errorf("failed to write sql file: %w", err))
	}

	fmt.Printf("Successfully generated seed_users.json and scripts/seed.sql for %d users.\n", NumUsers)
}
