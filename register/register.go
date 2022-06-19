package register

import (
	"database/sql"

	_ "github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
)

func RegisterUser(username string, password string) (string, error) {

	db, err := sql.Open("mysql", "sample_db_user:EXAMPLE_PASSWORD@tcp(host.docker.internal:33061)/sample_db")
	if err != nil {
		return "", err
	}
	defer db.Close()
	// make sure connection is available
	err = db.Ping()
	if err != nil {
		return "", err
	}

	queryString := "insert into system_users(username, password) values (?, ?)"

	stmt, err := db.Prepare(queryString)

	if err != nil {
		return "", err
	}

	defer stmt.Close()

	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(password), 14)

	_, err = stmt.Exec(username, hashedPassword)

	if err != nil {
		return "", err
	}

	return "success", nil
}
