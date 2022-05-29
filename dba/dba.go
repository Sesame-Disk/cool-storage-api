package dba

import (
	"cool-storage-api/authenticate"
	"database/sql"
)

type Contacto struct {
	Username, Password string
	User_id            int
}

func ObtenerBaseDeDatos() (db *sql.DB, e error) {
	db, err := sql.Open("mysql", "sample_db_user:EXAMPLE_PASSWORD@tcp(127.0.0.1:3306)/sample_db")
	if err != nil {
		return nil, err
	}
	return db, nil
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

	tokenDetails, err := authenticate.BuildRandomToken()
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
