package main

import "testing"

type TestChunks []string

func (tc TestChunks) Len() int {
	return len(tc)
}

func (tc TestChunks) Chunk(i int) string {
	return tc[i]
}

func TestBatches(t *testing.T) {

	autoerr := func(err error) {
		if err != nil {
			t.Error(err)
		}
	}

	t.Run("empty chunk list", func(t *testing.T) {
		tc := TestChunks{}
		b, err := Batches(tc, func(_, _ int) int { return 1 })
		autoerr(err)
		if len(b) != 0 {
			t.Errorf("expected an empty result, got %v", b)
		}
	})

	t.Run("single chunk under the limit", func(t *testing.T) {
		e := "abcd"
		l := 5
		tc := TestChunks{e}
		b, err := Batches(tc, func(_, _ int) int { return l })
		autoerr(err)
		if len(b) != 1 || len(b[0]) != 1 || b[0][0] != e {
			t.Errorf("expected a single batch with '%s', got %v", e, b)
		}
	})

	t.Run("single chunk over the limit", func(t *testing.T) {
		e := "abcd"
		l := 3
		tc := TestChunks{e}
		_, err := Batches(tc, func(_, _ int) int { return l })
		if err == nil {
			t.Errorf("expected an error about '%s' being longer than %d", e, l)
		}
	})

	t.Run("two chunks over the limit", func(t *testing.T) {
		l := 5
		tc := TestChunks{"abcd", "efgh"}
		b, err := Batches(tc, func(_, _ int) int { return l })
		autoerr(err)
		if len(b) != 2 || len(b[0]) != 1 || len(b[1]) != 1 {
			t.Errorf("expected two batches %v, got %v", tc, b)
		}
	})

	t.Run("four unequal chunks one time over the limit", func(t *testing.T) {
		l := 10
		tc := TestChunks{"abcd", "efg", "ijklmn", "opq"}
		b, err := Batches(tc, func(_, _ int) int { return l })
		autoerr(err)
		if len(b) != 2 || len(b[0]) != 2 || len(b[1]) != 2 || b[0][0] != tc[0] ||
			b[0][1] != tc[1] || b[1][0] != tc[2] || b[1][1] != tc[3] {
			t.Errorf("expected two batches %v, got %v", tc, b)
		}
	})

}
