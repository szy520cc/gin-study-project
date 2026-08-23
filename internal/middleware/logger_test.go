package middleware

import "testing"

func TestMaskQuery(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"敏感参数被替换", "username=admin&password=p%40ss", "password=%2A%2A%2A&username=admin"},
		{"变体也覆盖", "accessToken=abc&x=1", "accessToken=%2A%2A%2A&x=1"},
		{"普通参数保留", "page=2&page_size=10", "page=2&page_size=10"},
		{"无法解析时不回原串", "a=%zz&password=leak", "(unparsable query, masked)"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := maskQuery(c.raw); got != c.want {
				t.Errorf("maskQuery(%q) = %q, want %q", c.raw, got, c.want)
			}
		})
	}
}
