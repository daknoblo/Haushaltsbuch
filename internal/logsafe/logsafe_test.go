package logsafe

import "testing"

// A forged entry is the whole point: without stripping, a path can close the
// current line and write a second one that reads like the application's own.
func TestValueRefusesToStartANewLine(t *testing.T) {
	forged := "/x\nlevel=ERROR msg=\"transfer approved\""
	got := Value(forged)
	if got == forged {
		t.Fatal("the newline survived")
	}
	if want := "/xlevel=ERROR msg=\"transfer approved\""; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestValueFlattensEveryBreak(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"/a\rb", "/ab"},
		{"/a\r\nb", "/ab"},
		{"/a\tb", "/a b"},
		{"/plain", "/plain"},
		{"", ""},
		{"Umlaute ä ö ü bleiben", "Umlaute ä ö ü bleiben"},
	} {
		if got := Value(c.in); got != c.want {
			t.Errorf("Value(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
