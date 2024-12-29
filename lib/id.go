package lib

import (
	"github.com/aidarkhanov/nanoid"
)

// GenerateID generates a random ID with the default alphabet and length of 8 or an overridden length.
func GenID(length ...int) (string, error) {
	if length == nil {
		length = append(length, 12)
	}
	id, err := nanoid.Generate(nanoid.DefaultAlphabet, length[0])
	if err != nil {
		return "", err
	}
	return id, nil
}
