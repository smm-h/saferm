package trace

// The identifier profile, parsing side only. saferm never mints one.
//
// The profile is strict on purpose: a lenient parser is how one identifier
// becomes two strings that fail to link. Every rule below is either a rejection
// the base ULID specification leaves optional, or a layout fact the partition
// lookup depends on.

// crockford is the exact alphabet: no I, L, O or U.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// ulidLen is the identifier's exact length in characters.
const ulidLen = 26

// ulidTimestamp parses text under the strict profile and returns the
// millisecond embedded in it.
//
// Rejected, never repaired: any length but 26, any character outside the
// canonical uppercase alphabet (lowercase included -- one identifier has
// exactly one spelling), and a value that overflows 128 bits. 26 base32
// characters carry 130 bits while a ULID is 128, so the two leading bits are
// padding and the first character must not exceed '7'.
//
// The first ten characters carry those two padding bits plus the whole 48-bit
// timestamp, which is why the millisecond is read off them alone.
func ulidTimestamp(text string) (int64, bool) {
	if len(text) != ulidLen {
		return 0, false
	}
	var ms int64
	for i := 0; i < ulidLen; i++ {
		index := indexInAlphabet(text[i])
		if index < 0 {
			return 0, false
		}
		if i == 0 && index > 7 {
			return 0, false // overflows 128 bits
		}
		if i < 10 {
			ms = ms<<5 | int64(index)
		}
	}
	return ms, true
}

// indexInAlphabet returns c's value in the Crockford alphabet, or -1.
func indexInAlphabet(c byte) int {
	for i := 0; i < len(crockford); i++ {
		if crockford[i] == c {
			return i
		}
	}
	return -1
}
