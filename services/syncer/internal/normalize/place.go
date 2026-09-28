package normalize

import (
	"strings"

	"github.com/google/uuid"
)

// Changing the namespace would give every catalog row a new identity.
var idNamespace = uuid.MustParse("ebbbeec7-8d1a-4321-9834-cdcd0b15c3c8")

// EntityID is the stable catalog id of a source entity.
func EntityID(externalID string) uuid.UUID {
	return uuid.NewSHA1(idNamespace, []byte(externalID))
}

// NormalizedTitle is the form titles are compared in: lower case, ё as е, single spaces.
func NormalizedTitle(title string) string {
	lower := strings.ReplaceAll(strings.ToLower(title), "ё", "е")
	return strings.Join(strings.Fields(lower), " ")
}
