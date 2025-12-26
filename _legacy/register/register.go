package register

import (
	"cool-storage-api/dba"

	_ "github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
)

func RegisterUser(email string, password string) (string, error) {

	// db, err := sql.Open("mysql", "sample_db_user:EXAMPLE_PASSWORD@tcp(host.docker.internal:33061)/sample_db")
	db, err := dba.ObtenerBaseDeDatos()
	if err != nil {
		return "", err
	}
	defer db.Close()
	// make sure connection is available
	err = db.Ping()
	if err != nil {
		return "", err
	}

	// queryString := "insert into system_users(username, password) values (?, ?)"
	queryString := "insert into system_users(email, password, is_staff, name, avatar_url, quota_total, space_usage, organization_org_id) values (?, ?, ?, ?, ?, ?, ?, ?)"

	stmt, err := db.Prepare(queryString)
	if err != nil {
		return "", err
	}

	defer stmt.Close()

	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(password), 14)

	// _, err = stmt.Exec(email, hashedPassword)
	_, err = stmt.Exec(email, hashedPassword, "no", "", "", 10, 0, 1)
	if err != nil {
		return "", err
	}

	return "success", nil
}
