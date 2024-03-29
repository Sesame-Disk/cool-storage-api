package authenticate

import (
	"cool-storage-api/dba"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
)

//Get the username associated with the token input
func ValidateToken(authToken string) (map[string]interface{}, error) {

	// db, err := sql.Open("mysql", "sample_db_user:EXAMPLE_PASSWORD@tcp(host.docker.internal:33061)/sample_db")
	db, err := dba.ObtenerBaseDeDatos()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	// make sure connection is available
	err = db.Ping()
	if err != nil {
		return nil, err
	}

	queryString := `select 
                system_users.user_id,
                email,
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
	email := ""
	generatedAt := ""
	expiresAt := ""

	err = stmt.QueryRow(authToken).Scan(&userId, &email, &generatedAt, &expiresAt)

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
		"email":        email,
		"generated_at": generatedAt,
		"expires_at":   expiresAt,
	}

	return userDetails, nil
}

//Get a valid token associated with username and password
func GetToken(email string, password string) (map[string]string, error) {

	// db, err := sql.Open("mysql", "sample_db_user:EXAMPLE_PASSWORD@tcp(host.docker.internal:33061)/sample_db")
	db, err := dba.ObtenerBaseDeDatos()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	// make sure connection is available
	err = db.Ping()
	if err != nil {
		return nil, err
	}

	queryString := "select user_id, password from system_users where email = ?"

	stmt, err := db.Prepare(queryString)
	if err != nil {
		return nil, err
	}

	defer stmt.Close()

	userId := 0
	accountPassword := ""

	err = stmt.QueryRow(email).Scan(&userId, &accountPassword)

	if err != nil {

		if err == sql.ErrNoRows {
			return nil, errors.New("invalid email or password")
		}

		return nil, err
	}

	err = bcrypt.CompareHashAndPassword([]byte(accountPassword), []byte(password))

	if err != nil {
		return nil, errors.New("invalid email or password")
	}

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

			tokenDetails, err := BuildRandomToken()
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

		tokenDetails, err := BuildRandomToken()
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

	tokenDetails := map[string]string{
		"token_type":   "Bearer",
		"auth_token":   auth_token,
		"generated_at": generatedAt,
		"expires_at":   expiresAt,
	}

	return tokenDetails, nil
}

//Generate a random alphanumeric token of len 40
func BuildRandomToken() (map[string]string, error) {
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
	expirtyTime := time.Now().Add(time.Minute * 1440)

	generatedAt := dt.Format(timeLayout)
	expiresAt := expirtyTime.Format(timeLayout)

	tokenDetails := map[string]string{
		"token_type":   "Bearer",
		"auth_token":   authToken,
		"generated_at": generatedAt,
		"expires_at":   expiresAt,
	}

	return tokenDetails, err
}
