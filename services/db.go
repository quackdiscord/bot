package services

import (
	"database/sql"
	"os"
	"time"

	_ "github.com/go-sql-driver/mysql"
	log "github.com/sirupsen/logrus"
)

var DB *sql.DB

func ConnectDB() {
	// connect to the database
	db, err := sql.Open("mysql", os.Getenv("DATABASE_DSN"))
	if err != nil {
		log.Fatalf("Failed to connect to the database: %v", err)
	}

	// See "Important settings" section.
	db.SetConnMaxLifetime(time.Minute * 3)
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(10)

	err = db.Ping()
	if err != nil {
  		log.Fatalf("Failed to ping the database: %v", err)
	}

	// set the database
	DB = db
}
