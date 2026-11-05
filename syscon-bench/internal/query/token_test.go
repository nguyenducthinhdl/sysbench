package query

import "testing"

func TestTokens(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"ab cd 12 !!!", nil},
		{"INFO", []string{"INFO"}},
		{"error_code", []string{"error", "code"}},
		{"foo-bar baz", nil},
		{"long-token", []string{"long", "token"}},
		{"checkout info request", []string{"checkout", "info", "request"}},
		{"ErrorCode", []string{"ErrorCode"}},
		{"café", []string{"café"}},
		{"xx café yy", []string{"café"}},
		{"1234 200", []string{"1234"}},
		{"checkout info exception\n\tat checkout.api.Worker", []string{"checkout", "info", "exception", "checkout", "Worker"}},
	}
	for _, tc := range cases {
		got := Tokens(tc.in)
		if len(got) != len(tc.want) {
			t.Fatalf("%q got %q want %q", tc.in, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("%q got %q want %q", tc.in, got, tc.want)
			}
		}
	}
}

func TestCollectTokensCaps(t *testing.T) {
	msg := "aaaa bbbb cccc dddd eeee ffff gggg hhhh iiii jjjj"
	got := collectTokens(msg, 8)
	if len(got) != 8 || got[0] != "aaaa" || got[7] != "hhhh" {
		t.Fatalf("%q", got)
	}
	if len(Tokens(msg)) != 10 {
		t.Fatalf("all %q", Tokens(msg))
	}
}
