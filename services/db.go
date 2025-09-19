package services

import (
	"database/sql"
	"os"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/rs/zerolog/log"
)

var DB *sql.DB

func ConnectDB() {
	// start a connection pool
	db, err := sql.Open("mysql", os.Getenv("DATABASE_DSN"))
	if err != nil {
		log.Fatal().AnErr("Failed to connect to database", err)
	}

	//important settings ig
	db.SetConnMaxLifetime(time.Minute * 3)
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(10)

	// ping the database to make sure it's connected
	err = db.Ping()
	if err != nil {
		log.Fatal().AnErr("Failed to ping database", err)
	}

	log.Info().Msg("Connected to database")

	// set the database
	DB = db
}

func DisconnectDB() {
	DB.Close()
	log.Info().Msg("Disconnected from database")
}
