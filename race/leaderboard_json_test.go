package race

import "testing"

func TestMiiName(t *testing.T) {
	info := make([]byte, 0x50)
	for i, r := range []rune("Ana") {
		info[2+i*2+1] = byte(r)
	}
	if got := miiName(info); got != "Ana" {
		t.Fatalf("miiName = %q", got)
	}
	if miiName(nil) != "" {
		t.Fatal("short blob")
	}
}
