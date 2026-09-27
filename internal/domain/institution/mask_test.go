package institution

import "testing"

func TestMaskPhone(t *testing.T) {
	s := func(v string) *string { return &v }
	cases := []struct {
		in   *string
		want string
	}{
		{s("+91 98765 43412"), "+91 98••• ••412"},
		{s("9876543412"), "98••• ••412"},
		{s("12345"), "•••"},
	}
	for _, c := range cases {
		if got := maskPhone(c.in); got == nil || *got != c.want {
			t.Errorf("maskPhone(%q) = %v, want %q", *c.in, got, c.want)
		}
	}
	if maskPhone(nil) != nil {
		t.Error("maskPhone(nil) should stay nil")
	}
}
