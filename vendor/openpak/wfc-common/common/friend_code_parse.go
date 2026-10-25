package common

import "strings"

// ParseFriendCode turns a friend code as a player reads it off the screen ("1234-5678-9012",
// spaces or no separators) into the two raw values it could stand for. Most games print the
// raw code; the games getCRCType marks as reversed print its digits back to front, so both
// readings are returned and the caller checks which one the profile's game code verifies.
func ParseFriendCode(s string) (raw, reversed uint64, ok bool) {
	digits := strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(s))
	if len(digits) != 12 {
		return 0, 0, false
	}
	var rev [12]byte
	for i := 0; i < 12; i++ {
		c := digits[i]
		if c < '0' || c > '9' {
			return 0, 0, false
		}
		raw = raw*10 + uint64(c-'0')
		rev[11-i] = c
	}
	for _, c := range rev {
		reversed = reversed*10 + uint64(c-'0')
	}
	return raw, reversed, true
}

// FriendCodeMatches reports whether a code the player typed is the friend code of profile
// pid in the game gsbrcd, in either digit order.
func FriendCodeMatches(typed string, pid uint32, gsbrcd string) bool {
	raw, reversed, ok := ParseFriendCode(typed)
	if !ok || len(gsbrcd) < 4 {
		return false
	}
	want := CalcFriendCode(pid, gsbrcd)
	return want == raw || want == reversed
}
