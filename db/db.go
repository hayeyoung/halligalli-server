package db

import (
	"database/sql"
	"log"

	_ "github.com/lib/pq"
)

var DB *sql.DB

func Init() {
	var err error
	DB, err = sql.Open("postgres", "user=myuser password=987654 dbname=mydb sslmode=disable")
	if err != nil {
		log.Printf("❌ DB 연결 실패: %v", err)
		DB = nil
		return
	}

	if err := DB.Ping(); err != nil {
		log.Printf("❌ DB Ping 실패: %v", err)
		DB.Close()
		DB = nil
		return
	}

	log.Println("✅ DB 연결 성공")
}
