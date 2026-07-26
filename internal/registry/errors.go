package registry

import (
	"errors"
	"fmt"
	"net/http"

	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote/errcode"
)

var (
	ErrNotFound     = errors.New("registry: not found")
	ErrUnauthorized = errors.New("registry: unauthorized")
)

func mapError(err error, ref Reference) error {
	if errors.Is(err, errdef.ErrNotFound) {
		return fmt.Errorf("%w: %s", ErrNotFound, ref)
	}

	var resp *errcode.ErrorResponse
	if errors.As(err, &resp) {
		switch resp.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Errorf("%w: %s (try 'docker login %s')", ErrUnauthorized, ref, ref.Registry)
		case http.StatusNotFound:
			return fmt.Errorf("%w: %s", ErrNotFound, ref)
		}
	}

	return err
}
