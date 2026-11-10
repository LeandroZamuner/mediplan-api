package token

import "testing"

func TestGenerator(t *testing.T) {
	t.Run("dado un generador, cuando crea dos tokens, entonces son distintos y el hash corresponde al token", func(t *testing.T) {
		// Given
		g := Generator{}
		// When
		raw1, hash1, err1 := g.New()
		raw2, _, err2 := g.New()
		// Then
		if err1 != nil || err2 != nil {
			t.Fatal(err1, err2)
		}
		if raw1 == raw2 || raw1 == hash1 || g.Hash(raw1) != hash1 || len(raw1) < 40 {
			t.Fatalf("tokens inconsistentes: %q %q %q", raw1, raw2, hash1)
		}
	})
}
