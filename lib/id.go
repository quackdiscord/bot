package lib

import (
	"github.com/oklog/ulid/v2"
)

// simple helper so id creation stays consistent everywhere
func NewULID() (string, error) {
	return ulid.Make().String(), nil
}
