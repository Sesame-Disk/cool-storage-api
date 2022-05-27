package authenticate

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
)

func GenerateToken(username string, password string) (map[string]interface{}, error) {

	db, err := sql.Open("mysql", "sample_db_user:EXAMPLE_PASSWORD@tcp(127.0.0.1:3306)/sample_db")
	if err != nil {
		return nil, err
	}

	queryString := "select user_id, password from system_users where username = ?"

	stmt, err := db.Prepare(queryString)

	if err != nil {
		return nil, err
	}

	defer stmt.Close()

	userId := 0
	accountPassword := ""

	err = stmt.QueryRow(username).Scan(&userId, &accountPassword)

	if err != nil {

		if err == sql.ErrNoRows {
			return nil, errors.New("invalid username or password")
		}

		return nil, err
	}

	err = bcrypt.CompareHashAndPassword([]byte(accountPassword), []byte(password))

	if err != nil {
		return nil, errors.New("invalid username or password")
	}

	queryString = "insert into authentication_tokens(user_id, auth_token, generated_at, expires_at) values (?, ?, ?, ?)"
	stmt, err = db.Prepare(queryString)

	if err != nil {
		return nil, err
	}

	defer stmt.Close()

	randomToken := make([]byte, 32)

	_, err = rand.Read(randomToken)

	if err != nil {
		return nil, err
	}

	authToken := base64.URLEncoding.EncodeToString(randomToken)

	const timeLayout = "2006-01-02 15:04:05"

	dt := time.Now()
	expirtyTime := time.Now().Add(time.Minute * 1)

	generatedAt := dt.Format(timeLayout)
	expiresAt := expirtyTime.Format(timeLayout)

	_, err = stmt.Exec(userId, authToken, generatedAt, expiresAt)

	if err != nil {
		return nil, err
	}

	tokenDetails := map[string]interface{}{
		"token_type":   "Bearer",
		"auth_token":   authToken,
		"generated_at": generatedAt,
		"expires_at":   expiresAt,
	}

	return tokenDetails, nil
}

func ValidateToken(authToken string) (map[string]interface{}, error) {

	db, err := sql.Open("mysql", "sample_db_user:EXAMPLE_PASSWORD@tcp(127.0.0.1:3306)/sample_db")

	if err != nil {
		return nil, err
	}

	queryString := `select 
                system_users.user_id,
                username,
                generated_at,
                expires_at                         
            from authentication_tokens
            left join system_users
            on authentication_tokens.user_id = system_users.user_id
            where auth_token = ?`

	stmt, err := db.Prepare(queryString)

	if err != nil {
		return nil, err
	}

	defer stmt.Close()

	userId := 0
	username := ""
	generatedAt := ""
	expiresAt := ""

	err = stmt.QueryRow(authToken).Scan(&userId, &username, &generatedAt, &expiresAt)

	if err != nil {

		if err == sql.ErrNoRows {
			return nil, errors.New("invalid access token")
		}

		return nil, err
	}

	const timeLayout = "2006-01-02 15:04:05"

	expiryTime, _ := time.Parse(timeLayout, expiresAt)
	currentTime, _ := time.Parse(timeLayout, time.Now().Format(timeLayout))

	if expiryTime.Before(currentTime) {
		return nil, errors.New("the token is expired")
	}

	userDetails := map[string]interface{}{
		"user_id":      userId,
		"username":     username,
		"generated_at": generatedAt,
		"expires_at":   expiresAt,
	}

	return userDetails, nil
}

func GetToken(username string, password string) (map[string]interface{}, error) {

	db, err := sql.Open("mysql", "sample_db_user:EXAMPLE_PASSWORD@tcp(127.0.0.1:3306)/sample_db")
	if err != nil {
		return nil, err
	}

	queryString := "select user_id, password from system_users where username = ?"

	stmt, err := db.Prepare(queryString)

	if err != nil {
		return nil, err
	}

	defer stmt.Close()

	userId := 0
	accountPassword := ""

	err = stmt.QueryRow(username).Scan(&userId, &accountPassword)

	if err != nil {

		if err == sql.ErrNoRows {
			return nil, errors.New("invalid username or password" + username + " " + password)
		}

		return nil, err
	}

	err = bcrypt.CompareHashAndPassword([]byte(accountPassword), []byte(password))

	if err != nil {
		return nil, errors.New("invalid username or password" + username + " " + password)
	}

	//////////////////////////////////////////////////
	queryString = "select token_id, auth_token, generated_at, expires_at from authentication_tokens where user_id = ?"

	stmt, err = db.Prepare(queryString)

	if err != nil {
		return nil, err
	}

	defer stmt.Close()

	token_id := ""
	auth_token := ""
	generatedAt := ""
	expiresAt := ""

	err = stmt.QueryRow(userId).Scan(&token_id, &auth_token, &generatedAt, &expiresAt)
	if err != nil {

		if err == sql.ErrNoRows {

			queryString = "insert into authentication_tokens(user_id, auth_token, generated_at, expires_at) values (?, ?, ?, ?)"
			stmt, err = db.Prepare(queryString)

			if err != nil {
				return nil, err
			}

			defer stmt.Close()

			tokenDetails, err := buildRandomToken()
			if err != nil {
				return nil, err
			}

			_, err = stmt.Exec(userId, tokenDetails["auth_token"], tokenDetails["generated_at"], tokenDetails["expires_at"])
			if err != nil {
				return nil, err
			}
			return tokenDetails, err
		}
		return nil, err
	}

	const timeLayout = "2006-01-02 15:04:05"
	expiryTime, _ := time.Parse(timeLayout, expiresAt)
	currentTime, _ := time.Parse(timeLayout, time.Now().Format(timeLayout))

	if expiryTime.Before(currentTime) {
		// return nil, errors.New("The token is expired.\r\n")
		//we have to update old token version by token_id

		tokenDetails, err := buildRandomToken()
		if err != nil {
			return nil, err
		}

		sentenciaPreparada, err := db.Prepare("UPDATE authentication_tokens SET auth_token = ?, generated_at = ?, expires_at = ? WHERE token_id = ?")
		if err != nil {
			return nil, err
		}
		defer sentenciaPreparada.Close()
		// Pasar argumentos en el mismo orden que la consulta
		_, err = sentenciaPreparada.Exec(tokenDetails["auth_token"], tokenDetails["generated_at"], tokenDetails["expires_at"], token_id)
		if err != nil {
			return nil, err
		}
		return tokenDetails, err
	}

	tokenDetails := map[string]interface{}{
		"token_type":   "Bearer",
		"auth_token":   auth_token,
		"generated_at": generatedAt,
		"expires_at":   expiresAt,
	}

	return tokenDetails, nil
}

func buildRandomToken() (map[string]interface{}, error) {
	randomToken := make([]byte, 30)

	_, err := rand.Read(randomToken)

	if err != nil {
		return nil, err
	}

	authToken := base64.URLEncoding.EncodeToString(randomToken)

	authToken = strings.Replace(authToken, "-", "0", 40)
	authToken = strings.Replace(authToken, "_", "1", 40)

	const timeLayout = "2006-01-02 15:04:05"

	dt := time.Now()
	expirtyTime := time.Now().Add(time.Minute * 1)

	generatedAt := dt.Format(timeLayout)
	expiresAt := expirtyTime.Format(timeLayout)
	tokenDetails := map[string]interface{}{
		"token_type":   "Bearer",
		"auth_token":   authToken,
		"generated_at": generatedAt,
		"expires_at":   expiresAt,
	}

	return tokenDetails, err
}
