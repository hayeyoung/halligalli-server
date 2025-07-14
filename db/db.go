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
		log.Fatal("❌ DB 연결 실패:", err)
	}

	if err := DB.Ping(); err != nil {
		log.Fatal("❌ DB Ping 실패:", err)
	}

	log.Println("✅ DB 연결 성공")
}
