package postgres

import (
	"fmt"

	"github.com/google/uuid"
)

// newID returns the identifier for a new row.
//
// Identifiers are generated here rather than by a column default, because PostgreSQL 17 has no
// uuidv7() - it arrived in 18 - and v7 is what this schema wants. A v7 identifier carries a
// timestamp in its high bits, so it is time-ordered: inserts land at the right edge of the primary
// key's B-tree instead of scattering across random pages, and ORDER BY id follows creation order.
//
// The trade-off is deliberate and worth knowing: unlike v4, a v7 identifier reveals when the row
// was created. Nothing here treats an identifier as a secret, so that is acceptable.
func newID() (uuid.UUID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("generate uuid v7: %w", err)
	}
	return id, nil
}
