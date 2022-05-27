package register_test

import (
	"cool-storage-api/register"
	"database/sql"
	"math/rand"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestRegisterUser_WithRandomUser(t *testing.T) {
	rand.Seed(time.Now().UnixNano())
	randomUser := strconv.Itoa(rand.Intn(1000000))
	randomPassword := strconv.Itoa(rand.Intn(1000000))
	expectation := "success"

	var count int

	db, err := sql.Open("mysql", "sample_db_user:EXAMPLE_PASSWORD@tcp(127.0.0.1:3306)/sample_db")
	if err != nil {
		t.Errorf(err.Error())
	}

	dbCount := func(db *sql.DB) (int, error) {
		queryString := "SELECT COUNT(user_id) AS NumberOfUsers FROM system_users"

		stmt, err := db.Prepare(queryString)
		if err != nil {
			return -1, err
		}

		defer stmt.Close()

		err = stmt.QueryRow().Scan(&count)
		if err != nil {
			return -1, err
		}
		return count, nil
	}

	oldCount, err1 := dbCount(db)
	if err1 != nil {
		t.Errorf(err.Error())
	}

	result, err := register.RegisterUser(randomUser, randomPassword)
	if result == "" || err != nil {
		t.Errorf("Expected %v,%v but got %v,%v", expectation, nil, result, err)
	}

	newCount, err2 := dbCount(db)
	if err2 != nil {
		t.Errorf(err.Error())
	}

	if newCount != oldCount+1 {
		t.Errorf("Expected %v but got %v", (oldCount + 1), newCount)
	}

	queryString := "select password from system_users where username = ?"

	stmt, err := db.Prepare(queryString)

	if err != nil {
		t.Errorf(err.Error())
	}

	defer stmt.Close()

	accountPassword := ""

	err = stmt.QueryRow(randomUser).Scan(&accountPassword)

	if err != nil {
		t.Errorf("Expected %v but got %v", nil, err.Error())
	}

	err = bcrypt.CompareHashAndPassword([]byte(accountPassword), []byte(randomPassword))

	if err != nil {
		t.Errorf("Expected %v but got %v for password %v and hashedPassword= %v", nil, err.Error(), randomPassword, accountPassword)
	}

}
