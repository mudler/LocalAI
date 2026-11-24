package connsets

import (
	"encoding/json"
	"testing"
)

func TestSetsReadsBothForms(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"single value migrates", `{"a":"b1"}`, `{"a":["b1"]}`},
		{"list", `{"a":["b1","b2"]}`, `{"a":["b1","b2"]}`},
		{"mixed", `{"a":"b1","c":["x"]}`, `{"a":["b1"],"c":["x"]}`},
		{"empty string is no choice", `{"a":""}`, `{}`},
		{"repeats and blanks go", `{"a":["b1","","b1","b2"]}`, `{"a":["b1","b2"]}`},
		{"null", `null`, `null`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var s Sets
			if err := json.Unmarshal([]byte(c.in), &s); err != nil {
				t.Fatal(err)
			}
			got, _ := json.Marshal(s)
			if string(got) != c.want {
				t.Fatalf("got %s want %s", got, c.want)
			}
		})
	}
	var s Sets
	if err := json.Unmarshal([]byte(`{"a":3}`), &s); err == nil {
		t.Fatal("a number must be refused")
	}
}

func TestCloneAndEqual(t *testing.T) {
	a := Sets{"x": {"1", "2"}}
	b := Clone(a)
	b["x"][0] = "changed"
	if a["x"][0] != "1" {
		t.Fatal("clone shares storage")
	}
	if Equal(a, b) || !Equal(a, Clone(a)) || Clone(Sets{}) != nil {
		t.Fatal("equal or clone wrong")
	}
}
