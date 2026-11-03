package character

import "testing"

func TestUnicodeIdentityPreservesProviderLookup(t *testing.T) {
	a, err := NormalizeKey("eu", "гордунни", "  КИПЕТ  ")
	if err != nil {
		t.Fatal(err)
	}
	b, err := NormalizeKey("eu", "гордунни", "кипет")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != b.Name || a.LookupName != "КИПЕТ" {
		t.Fatal("identity folding or original lookup lost")
	}
	c, err := NormalizeKey("eu", "synthetic", "Straße")
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "strasse" || c.LookupName != "Straße" {
		t.Fatal("case folding must not replace provider lookup")
	}
	d, err := NormalizeKey("eu", "synthetic", "E\u0301lan")
	if err != nil {
		t.Fatal(err)
	}
	e, _ := NormalizeKey("eu", "synthetic", "Élan")
	if d.Name != e.Name {
		t.Fatal("canonical equivalents differ")
	}
	for _, name := range []string{"a", "bad/name", "bad\\name", "foo bar", "\u0301aa", "abc%2fdef", "abc\x00"} {
		if _, err := NormalizeKey("eu", "synthetic", name); err == nil {
			t.Fatalf("accepted unsafe name %q", name)
		}
	}
	if _, err := NormalizeKey("kr", "synthetic", "Valid"); err == nil {
		t.Fatal("unsupported region accepted")
	}
}
