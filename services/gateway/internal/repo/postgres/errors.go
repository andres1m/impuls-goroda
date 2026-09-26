package postgres

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var ErrNotFound = errors.New("not found")

func mapQueryError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", operation, ErrNotFound)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
