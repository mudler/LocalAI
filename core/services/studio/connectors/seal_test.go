package connectors_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
)

func TestSealingHidesTheTokenAndBindsItToItsBinding(t *testing.T) {
	key, _ := connectors.NewKey()
	s, err := connectors.NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	token := []byte("123456:ABC-secret-token")
	aad := connectors.AAD("b1", "u1")
	a, _ := s.Seal(aad, token)
	b, _ := s.Seal(aad, token)
	if bytes.Equal(a, b) {
		t.Fatal("two seals of one token are equal")
	}
	if bytes.Contains(a, []byte("secret")) {
		t.Fatal("the token is in the sealed bytes")
	}
	if got, err := s.Open(aad, a); err != nil || !bytes.Equal(got, token) {
		t.Fatalf("open: %q %v", got, err)
	}
	if _, err := s.Open(connectors.AAD("b2", "u1"), a); err == nil {
		t.Fatal("a token opened for another binding")
	}
	a[len(a)-1] ^= 1
	if _, err := s.Open(aad, a); err == nil {
		t.Fatal("a changed value opened")
	}
	if _, err := s.Open(aad, []byte("x")); err == nil {
		t.Fatal("a short value opened")
	}
	if _, err := connectors.NewSealer([]byte("short")); err == nil {
		t.Fatal("a short key was accepted")
	}
}

func TestValidate(t *testing.T) {
	ok := connectors.Binding{Name: "Bot", Kind: connectors.KindTelegram, AgentID: "a", Allow: []string{"12", "-100123", "@alice_b"}, NotifyChat: "55"}
	if err := connectors.Validate(ok); err != nil {
		t.Fatal(err)
	}
	many := make([]string, connectors.MaxAllow+1)
	for i := range many {
		many[i] = "1"
	}
	for name, b := range map[string]connectors.Binding{
		"no name":    {Kind: connectors.KindTelegram, AgentID: "a"},
		"long name":  {Name: strings.Repeat("n", 81), Kind: connectors.KindTelegram, AgentID: "a"},
		"bad kind":   {Name: "b", Kind: "Not A Kind", AgentID: "a"},
		"no agent":   {Name: "b", Kind: connectors.KindTelegram},
		"bad allow":  {Name: "b", Kind: connectors.KindTelegram, AgentID: "a", Allow: []string{"alice"}},
		"short name": {Name: "b", Kind: connectors.KindTelegram, AgentID: "a", Allow: []string{"@ab"}},
		"many":       {Name: "b", Kind: connectors.KindTelegram, AgentID: "a", Allow: many},
		"bad notify": {Name: "b", Kind: connectors.KindTelegram, AgentID: "a", NotifyChat: "me"},
	} {
		if err := connectors.Validate(b); !errors.Is(err, connectors.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestAllowsMatchesByIDOrUsername(t *testing.T) {
	b := connectors.Binding{Allow: []string{"42", "@Alice"}}
	if !b.Allows("42", "") || !b.Allows("7", "alice") || b.Allows("7", "bob") || b.Allows("7", "") {
		t.Fatal("wrong allowlist match")
	}
	if (connectors.Binding{}).Allows("1", "x") {
		t.Fatal("an empty allowlist accepted someone")
	}
}
