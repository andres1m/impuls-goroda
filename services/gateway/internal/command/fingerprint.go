package command

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
)

type PathComponent struct {
	Name  string
	Value string
}

type FingerprintInput struct {
	ActorID          domain.UserID
	Operation        Operation
	Path             []PathComponent
	ExpectedRevision *domain.RouteRevisionNumber
	Body             any
}

func Fingerprint(input FingerprintInput) ([32]byte, error) {
	if input.ActorID == (domain.UserID{}) || !input.Operation.Valid() {
		return [32]byte{}, errors.New("actor and supported operation are required")
	}
	if input.ExpectedRevision != nil {
		if err := input.ExpectedRevision.Validate(); err != nil {
			return [32]byte{}, err
		}
	}
	body, err := json.Marshal(input.Body)
	if err != nil {
		return [32]byte{}, err
	}
	digest := sha256.New()
	writePart(digest, []byte(input.Operation))
	writePart(digest, input.ActorID[:])
	var count [8]byte
	binary.BigEndian.PutUint64(count[:], uint64(len(input.Path)))
	writePart(digest, count[:])
	for _, component := range input.Path {
		if component.Name == "" || component.Value == "" {
			return [32]byte{}, errors.New("path components must have names and values")
		}
		writePart(digest, []byte(component.Name))
		writePart(digest, []byte(component.Value))
	}
	if input.ExpectedRevision == nil {
		writePart(digest, []byte{0})
	} else {
		var encoded [9]byte
		encoded[0] = 1
		binary.BigEndian.PutUint64(encoded[1:], uint64(*input.ExpectedRevision))
		writePart(digest, encoded[:])
	}
	writePart(digest, body)
	var result [32]byte
	copy(result[:], digest.Sum(nil))
	return result, nil
}

func writePart(digest hash.Hash, part []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(part)))
	_, _ = digest.Write(size[:])
	_, _ = digest.Write(part)
}
