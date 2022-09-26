package dba

import (
	"cool-storage-api/configread"
	"cool-storage-api/util"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

type Contacto struct {
	Username, Password string
	User_id            int
}

func ObtenerBaseDeDatos() (db *sql.DB, e error) {

	config := configread.Configuration
	// db, err := sql.Open("mysql", "sample_db_user:EXAMPLE_PASSWORD@tcp(host.docker.internal:33061)/sample_db")
	// if err != nil {
	// 	return nil, err
	// }
	// err = db.Ping()
	// if err != nil {
	// 	return nil, err
	// }
	// return db, nil

	usuario := config.DataBaseConfig.Usuario                     //"root"
	pass := config.DataBaseConfig.Pass                           //"0204"
	host := config.DataBaseConfig.Host                           //"tcp(127.0.0.1:3306)"
	nombreBaseDeDatos := config.DataBaseConfig.NombreBaseDeDatos //"new_db_collection"

	// Debe tener la forma usuario:contraseña@protocolo(host:puerto)/nombreBaseDeDatos
	db, err := sql.Open("mysql", fmt.Sprintf("%s:%s@%s/%s", usuario, pass, host, nombreBaseDeDatos))
	if err != nil {
		return nil, err
	}
	// defer db.Close()
	// make sure connection is available
	err = db.Ping()
	if err != nil {
		return nil, err
	}
	return db, err
}

func Eliminar(c Contacto) error {
	db, err := ObtenerBaseDeDatos()
	if err != nil {
		return err
	}
	defer db.Close()

	sentenciaPreparada, err := db.Prepare("DELETE FROM system_users WHERE user_id = ?")
	if err != nil {
		return err
	}
	defer sentenciaPreparada.Close()

	_, err = sentenciaPreparada.Exec(c.User_id)
	if err != nil {
		return err
	}
	return nil
}

func Insertar(c Contacto) (e error) {
	db, err := ObtenerBaseDeDatos()
	if err != nil {
		return err
	}
	defer db.Close()

	// Preparamos para prevenir inyecciones SQL
	sentenciaPreparada, err := db.Prepare("INSERT INTO system_users (username, password) VALUES(?, ?)")
	if err != nil {
		return err
	}
	defer sentenciaPreparada.Close()
	// Ejecutar sentencia, un valor por cada '?'
	_, err = sentenciaPreparada.Exec(c.Username, c.Password)
	if err != nil {
		return err
	}
	return nil
}

func InsertIntoAuthenticationTokens() (e error) {
	db, err1 := ObtenerBaseDeDatos()
	if err1 != nil {
		return err1
	}
	defer db.Close()

	queryString := "insert into authentication_tokens(user_id, auth_token, generated_at, expires_at) values (?, ?, ?, ?)"
	stmt, err := db.Prepare(queryString)
	if err != nil {
		return err
	}

	defer stmt.Close()

	tokenDetails, err := BuildRandomToken()
	if err != nil {
		return err
	}
	user_id := 100000000
	_, err = stmt.Exec(user_id, tokenDetails["auth_token"], tokenDetails["generated_at"], tokenDetails["expires_at"])
	if err != nil {
		return err
	}
	return nil
}

func InsertArchive(a util.Archive) (e error) {
	db, err := ObtenerBaseDeDatos()
	if err != nil {
		return err
	}
	defer db.Close()

	sql, err := db.Prepare("INSERT INTO files (`vault_file_id`,`library_id`,`user_id`,`file_name`,`upload_date`,`file_size`,`file_state`) VALUES(?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer sql.Close()

	_, err = sql.Exec(a.Vault_file_id, a.Library_id, a.User_id, a.File_name, a.Upload_date, a.File_size, a.File_state)
	if err != nil {
		return err
	}

	return nil
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
	expirtyTime := time.Now().Add(time.Minute * 1)

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
