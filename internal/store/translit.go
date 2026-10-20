package store

import "strings"

// Serbian is written in two alphabets. The index carries a Latin copy of
// Cyrillic text and a query in either alphabet is tried in both, so "rачун"
// and "racun" find the same document (diacritics are folded by the tokenizer).

var cyrToLat = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'ђ': "đ", 'е': "e", 'ж': "ž", 'з': "z", 'и': "i", 'ј': "j", 'к': "k", 'л': "l", 'љ': "lj", 'м': "m",
	'н': "n", 'њ': "nj", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'ћ': "ć", 'у': "u", 'ф': "f", 'х': "h", 'ц': "c", 'ч': "č", 'џ': "dž", 'ш': "š",
	'А': "A", 'Б': "B", 'В': "V", 'Г': "G", 'Д': "D", 'Ђ': "Đ", 'Е': "E", 'Ж': "Ž", 'З': "Z", 'И': "I", 'Ј': "J", 'К': "K", 'Л': "L", 'Љ': "Lj", 'М': "M",
	'Н': "N", 'Њ': "Nj", 'О': "O", 'П': "P", 'Р': "R", 'С': "S", 'Т': "T", 'Ћ': "Ć", 'У': "U", 'Ф': "F", 'Х': "H", 'Ц': "C", 'Ч': "Č", 'Џ': "Dž", 'Ш': "Š",
	// Russian/Macedonian letters that OCR may produce in Serbian text
	'й': "j", 'ы': "i", 'э': "e", 'ю': "ju", 'я': "ja", 'ь': "", 'ъ': "", 'щ': "šč", 'ё': "jo", 'ѓ': "đ", 'ќ': "ć", 'ѕ': "dz",
	'Й': "J", 'Ы': "I", 'Э': "E", 'Ю': "Ju", 'Я': "Ja", 'Ь': "", 'Ъ': "", 'Щ': "Šč", 'Ё': "Jo", 'Ѓ': "Đ", 'Ќ': "Ć", 'Ѕ': "Dz",
}

// HasCyrillic reports whether the text has any Cyrillic letter.
func HasCyrillic(s string) bool {
	for _, r := range s {
		if r >= 0x0400 && r <= 0x04FF {
			return true
		}
	}
	return false
}

// CyrToLat transliterates Serbian Cyrillic to Latin; other runes pass through.
func CyrToLat(s string) string {
	if !HasCyrillic(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if l, ok := cyrToLat[r]; ok {
			b.WriteString(l)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var latToCyr = map[string]string{
	"lj": "љ", "nj": "њ", "dž": "џ", "dz": "џ",
	"a": "а", "b": "б", "v": "в", "g": "г", "d": "д", "đ": "ђ", "dj": "ђ", "e": "е", "ž": "ж", "z": "з", "i": "и", "j": "ј", "k": "к", "l": "л", "m": "м",
	"n": "н", "o": "о", "p": "п", "r": "р", "s": "с", "t": "т", "ć": "ћ", "u": "у", "f": "ф", "h": "х", "c": "ц", "č": "ч", "š": "ш",
}

// LatToCyr transliterates Serbian Latin to Cyrillic (lowercase output), for
// trying a Latin query against Cyrillic text. Letters outside the Serbian
// alphabet (w, x, y, q) pass through, so it is only useful as a second try.
func LatToCyr(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if i+1 < len(rs) {
			if c, ok := latToCyr[string(rs[i:i+2])]; ok {
				b.WriteString(c)
				i++
				continue
			}
		}
		if c, ok := latToCyr[string(rs[i])]; ok {
			b.WriteString(c)
		} else {
			b.WriteRune(rs[i])
		}
	}
	return b.String()
}

// IsLatinSerbianish reports whether a query could be Serbian Latin: only
// letters of the Serbian Latin alphabet, digits and spaces.
func IsLatinSerbianish(s string) bool {
	has := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r == 'č', r == 'ć', r == 'đ', r == 'š', r == 'ž':
			if r == 'w' || r == 'x' || r == 'y' || r == 'q' {
				return false
			}
			has = true
		case r >= '0' && r <= '9', r == ' ', r == '"', r == '-':
		default:
			return false
		}
	}
	return has
}
