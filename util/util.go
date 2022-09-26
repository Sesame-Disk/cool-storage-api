package util

import (
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"os"
)

type Archive struct {
	Vault_file_id string
	Library_id    int
	User_id       int
	File_name     string
	Upload_date   string
	File_size     int
	File_state    string
}

func AppendData(path string, data []byte) {
	// If the file doesn't exist, create it, or append to the file
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		log.Fatal(err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}
}

func HashingReadFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		log.Fatal(err)
	}

	return fmt.Sprintf("%x", h.Sum(nil))
}
