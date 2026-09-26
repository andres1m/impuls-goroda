package postgres

import (
	"errors"

	"github.com/jackc/pgx/v5/pgtype"
)

func encodeUUID(id [16]byte) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

func decodeUUID(id pgtype.UUID) ([16]byte, error) {
	if !id.Valid {
		return [16]byte{}, errors.New("database returned a null UUID")
	}
	return id.Bytes, nil
}

func decodeHash(hash []byte) ([32]byte, error) {
	if len(hash) != 32 {
		return [32]byte{}, errors.New("database returned an invalid token hash")
	}
	var result [32]byte
	copy(result[:], hash)
	return result, nil
}
