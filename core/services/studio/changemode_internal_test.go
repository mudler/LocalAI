package studio

import (
	"strings"
	"testing"
)

func TestAutoTurnCap(t *testing.T) {
	turn := &autoTurn{}
	for i := range MaxAutoWritesPerTurn {
		if !turn.take() {
			t.Fatalf("write %d should be allowed", i+1)
		}
	}
	if turn.take() {
		t.Fatal("the write over the cap was allowed")
	}
	turn.give()
	if !turn.take() {
		t.Fatal("a returned reservation should be usable")
	}
	var none *autoTurn
	if none.take() {
		t.Fatal("no turn state means no automatic write")
	}
}

func TestAutoFits(t *testing.T) {
	cases := []struct {
		name, content string
		want          bool
	}{
		{"plain note", "- lives in Rome\n- prefers short answers\n", true},
		{"at the size cap", strings.Repeat("a", MaxAutoWriteBytes), true},
		{"over the size cap", strings.Repeat("a", MaxAutoWriteBytes+1), false},
		{"private key", "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n", false},
		{"api key", "work key sk-abcdefghijklmnopqrstuvwx", false},
		{"github token", "token ghp_abcdefghijklmnopqrstuvwxyz0123456789", false},
		{"cloud key id", "AKIAABCDEFGHIJKLMNOP", false},
		{"password line", "password: hunter2hunter2", false},
		{"the word password alone", "does not like password managers", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, why := autoFits(c.content, &autoTurn{})
			if got != c.want {
				t.Fatalf("autoFits = %v (%s), want %v", got, why, c.want)
			}
			if !got && why == "" {
				t.Fatal("a refusal must say why")
			}
		})
	}
}

func TestAutoFitsDoesNotSpendTheTurnOnARefusal(t *testing.T) {
	turn := &autoTurn{}
	for range 20 {
		autoFits(strings.Repeat("a", MaxAutoWriteBytes+1), turn)
	}
	for i := range MaxAutoWritesPerTurn {
		if ok, why := autoFits("- ok\n", turn); !ok {
			t.Fatalf("note %d refused: %s", i+1, why)
		}
	}
}
