package validator

import (
	"fmt"

	"github.com/go-playground/validator/v10"
	"github.com/labstack/echo/v5"
)

type Validator struct {
	validator *validator.Validate
}

var _ echo.Validator = (*Validator)(nil)

func NewValidator() *Validator {
	return &Validator{
		validator: validator.New(validator.WithRequiredStructEnabled()),
	}
}

func (v *Validator) Validate(i any) error {
	if err := v.validator.Struct(i); err != nil {
		return fmt.Errorf("validation error: %w", err)
	}

	return nil
}
