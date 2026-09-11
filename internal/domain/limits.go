package domain

import "math"

// Length bounds for text a client can send.
//
// The unit is bytes, the same unit the CHECK constraints in the migrations use (octet_length), so
// validation and the schema can never disagree: anything that passes Validate can always be
// stored, and a client mistake is answered with 400 instead of surfacing as a failed write.
//
// For that to hold, every bound is measured on the RAW value, never on a trimmed copy: the raw
// value is what the repository binds. Trimming answers only "is this blank".
//
// Names and addresses were bounded from the start. MaxFreeTextLen closes the fields that were not:
// an order comment of 60 000 characters used to be accepted and stored in full.
const (
	MaxNameLen     = 255
	MaxAddressLen  = 500
	MaxFreeTextLen = 1000
)

// fitsInt32 reports whether v can be stored in an INT column.
//
// Go's int is 64 bits, sort_order and stock_quantity are int4, and the driver refuses to encode
// anything wider - which would surface a client's oversized number as a 500 rather than a 400.
// price_cents needs no such check: that column is BIGINT.
func fitsInt32(v int) bool {
	return v >= math.MinInt32 && v <= math.MaxInt32
}
