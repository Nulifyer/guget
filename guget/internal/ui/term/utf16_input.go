package term

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// utf16InputDecoder retains a high surrogate when a console read ends midway
// through a supplementary character. Both console input paths use this decoder.
type utf16InputDecoder struct{ high uint16 }

func (d *utf16InputDecoder) decode(units []uint16, final bool) string {
	var out strings.Builder
	for _, unit := range units {
		if d.high != 0 {
			if unit >= 0xdc00 && unit <= 0xdfff {
				out.WriteRune(utf16.DecodeRune(rune(d.high), rune(unit)))
				d.high = 0
				continue
			}
			out.WriteRune(utf8.RuneError)
			d.high = 0
		}
		switch {
		case unit >= 0xd800 && unit <= 0xdbff:
			d.high = unit
		case unit >= 0xdc00 && unit <= 0xdfff:
			out.WriteRune(utf8.RuneError)
		default:
			out.WriteRune(rune(unit))
		}
	}
	if final && d.high != 0 {
		out.WriteRune(utf8.RuneError)
		d.high = 0
	}
	return out.String()
}
