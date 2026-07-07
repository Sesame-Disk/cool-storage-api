package db

import (
	"fmt"
	"strings"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

const PlainBlockRepresentationID = "plain:v1"

func EncryptedLibraryBlockRepresentationID(libraryID string) string {
	libraryID = strings.TrimSpace(libraryID)
	if libraryID == "" {
		return ""
	}
	return "library:" + libraryID
}

func EffectiveBlockRepresentationID(libraryID string, encrypted bool, stored string) string {
	stored = strings.TrimSpace(stored)
	if stored != "" {
		return stored
	}
	if encrypted {
		return EncryptedLibraryBlockRepresentationID(libraryID)
	}
	return PlainBlockRepresentationID
}

func ValidateBlockRepresentationID(representationID string) error {
	if strings.TrimSpace(representationID) == "" {
		return fmt.Errorf("missing block representation id")
	}
	return nil
}

func ResolveBlockRepresentationID(session *gocql.Session, orgID, libraryID string) (string, error) {
	state, err := ReadLiveLibraryState(session, orgID, libraryID)
	if err != nil {
		return "", err
	}
	return state.BlockRepresentationIDOrDefault(), nil
}

func ResolveBlockRepresentationIDByLibraryID(session *gocql.Session, libraryID string) (string, error) {
	state, err := ResolveLiveLibraryStateByID(session, libraryID)
	if err != nil {
		return "", err
	}
	return state.BlockRepresentationIDOrDefault(), nil
}