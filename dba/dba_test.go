package dba_test

import (
	"cool-storage-api/dba"
	"math/rand"
	"strconv"
	"testing"
	"time"
)

func TestObtenerBaseDeDatos(t *testing.T) {
	_, err := dba.ObtenerBaseDeDatos()
	if err != nil {
		t.Errorf("Expected %v but got %v", nil, err)
	}
}

func TestInsertar(t *testing.T) {
	rand.Seed(time.Now().UnixNano())
	randomUser := strconv.Itoa(rand.Intn(1000000))
	randomPassword := strconv.Itoa(rand.Intn(1000000))
	var c dba.Contacto
	c.Username = randomUser
	c.Password = randomPassword

	err := dba.Insertar(c)
	if err != nil {
		t.Errorf("Expected %v but got %v", nil, err)
	}
	dba.Eliminar(c)
}

func TestEliminar(t *testing.T) {
	rand.Seed(time.Now().UnixNano())
	randomUser := strconv.Itoa(rand.Intn(1000000))
	randomPassword := strconv.Itoa(rand.Intn(1000000))
	var c dba.Contacto
	c.Username = randomUser
	c.Password = randomPassword

	err := dba.Insertar(c)
	if err != nil {
		t.Errorf("Expected %v but got %v", nil, err)
	}
	err = dba.Eliminar(c)
	if err != nil {
		t.Errorf("Expected %v but got %v", nil, err)
	}
}

func TestInsertIntoAuthenticationTokens(t *testing.T) {
	err := dba.InsertIntoAuthenticationTokens()
	if err != nil {
		t.Errorf("Expected %v but got %v", nil, err)
	}
}
