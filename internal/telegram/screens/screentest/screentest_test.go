package screentest

import "testing"

func TestValidTelegramHTML(t *testing.T) {
	for _, tt := range []struct {
		name string
		text string
		ok   bool
	}{
		{"plain text, nothing to check", "hello", true},
		{"one balanced tag", "<b>hello</b>", true},
		{"nested, balanced", "<b>hello <i>world</i></b>", true},
		{"expandable blockquote", "<blockquote expandable>a\nb\nc</blockquote>", true},
		{"escaped ampersand", "Ada &amp; Sons", true},
		{"escaped angle brackets", "5 &lt; 10 &gt; 3", true},
		{"an allowed tag with an attribute", `<a href="https://t.me">link</a>`, true},
		{"unknown tag", "<script>evil</script>", false},
		{"unclosed tag", "<b>hello", false},
		{"mismatched close", "<b><i>hello</b></i>", false},
		{"close with nothing open", "hello</b>", false},
		{"bare ampersand", "Ada & Sons", false},
		{"bare angle bracket", "5 < 10", false},
		{"bare angle bracket, right side", "5 > 3", false},
		{"an ampersand that only looks like an entity", "A&Ben", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidTelegramHTML(tt.text)
			if tt.ok && err != nil {
				t.Errorf("ValidTelegramHTML(%q) = %v, want nil", tt.text, err)
			}
			if !tt.ok && err == nil {
				t.Errorf("ValidTelegramHTML(%q) = nil, want an error", tt.text)
			}
		})
	}
}

func TestStripHTMLForLint(t *testing.T) {
	for _, tt := range []struct{ text, want string }{
		{"hello", "hello"},
		{"<b>hello</b>", "hello"},
		{"<blockquote expandable>a\nb</blockquote>", "a\nb"},
		{"Ada &amp; Sons", "Ada & Sons"},
		{"5 &lt; 10 &gt; 3", "5 < 10 > 3"},
		{`<a href="https://t.me">link</a>`, "link"},
	} {
		if got := StripHTMLForLint(tt.text); got != tt.want {
			t.Errorf("StripHTMLForLint(%q) = %q, want %q", tt.text, got, tt.want)
		}
	}
}
