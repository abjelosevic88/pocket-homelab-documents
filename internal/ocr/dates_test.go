package ocr

import "testing"

func TestGuessCreated(t *testing.T) {
	for _, c := range []struct{ text, want string }{
		{"Rechnung vom 14.03.2024\nFällig am 28.03.2024", "2024-03-14"},
		{"Invoice date: 2023-11-02", "2023-11-02"},
		{"Banja Luka, 5. oktobar 2025.", "2025-10-05"},
		{"Бања Лука, 14. марта 2024. године", "2024-03-14"},
		{"Datum: 3. decembra 2023.", "2023-12-03"},
		{"Dated March 3, 2022 at noon", "2022-03-03"},
		{"Payment 03/14/2024 US style", "2024-03-14"},
		{"Valid until 31.12.2999 nothing else", ""},
		{"no dates here 12345", ""},
		{"Број: 06/1.07/0702-455-31/25\nДатум: 15.12.2025.", "2025-12-15"},
		{"Ref 12.3-2024 then 01.02.2024", "2024-02-01"},
	} {
		if got := GuessCreated(c.text, true); got != c.want {
			t.Errorf("%q: got %q want %q", c.text, got, c.want)
		}
	}
}
