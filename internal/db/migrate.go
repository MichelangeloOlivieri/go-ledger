package db

import (
	"log"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

// syncs the database schema with the current code version
func RunMigrations(migrationURL string, dbSource string) {
	// reads .sql (//:migrations) files from disk and opens a DB connection
	migration, err := migrate.New(migrationURL, dbSource)
	if err != nil {
		log.Fatalf("Failed to create migration instance: %v", err)
	}

	// executes all pending .up.sql files in chronological order
	err = migration.Up()
	if err != nil && err != migrate.ErrNoChange {
		log.Fatalf("Failed to run up-migrations: %v", err)
	}
	log.Println("Database migrations applied successfully.")
}
