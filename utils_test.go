package main

import "testing"

type TestChunks struct {
	Chunks []string
	Limit  int
	Index  int
}

func (tc *TestChunks) Next(nb_batches int) (string, int) {
	if tc.Index >= len(tc.Chunks) {
		return "", 0
	}
	str := tc.Chunks[tc.Index]
	tc.Index++
	return str, tc.Limit
}

func TestBatches(t *testing.T) {

	autoerr := func(err error) {
		if err != nil {
			t.Error(err)
		}
	}

	t.Run("empty chunk list", func(t *testing.T) {
		tc := &TestChunks{Chunks: []string{}, Limit: 1}
		b, err := Batches(tc)
		autoerr(err)
		if len(b) != 0 {
			t.Errorf("expected an empty result, got %v", b)
		}
	})

	t.Run("single chunk under the limit", func(t *testing.T) {
		e := "abcd"
		tc := &TestChunks{Chunks: []string{e}, Limit: 5}
		b, err := Batches(tc)
		autoerr(err)
		if len(b) != 1 || len(b[0]) != 1 || b[0][0] != e {
			t.Errorf("expected a single batch with '%s', got %v", e, b)
		}
	})

	t.Run("single chunk over the limit", func(t *testing.T) {
		e := "abcd"
		tc := &TestChunks{Chunks: []string{e}, Limit: 3}
		_, err := Batches(tc)
		if err == nil {
			t.Errorf("expected an error about '%s' being longer than %d", e, tc.Limit)
		}
	})

	t.Run("two chunks over the limit", func(t *testing.T) {
		tc := &TestChunks{Chunks: []string{"abcd", "efgh"}, Limit: 5}
		b, err := Batches(tc)
		autoerr(err)
		if len(b) != 2 || len(b[0]) != 1 || len(b[1]) != 1 {
			t.Errorf("expected two batches %v, got %v", tc, b)
		}
	})

	t.Run("four unequal chunks one time over the limit", func(t *testing.T) {
		tc := &TestChunks{Chunks: []string{"abcd", "efg", "ijklmn", "opq"}, Limit: 10}
		b, err := Batches(tc)
		autoerr(err)
		c := tc.Chunks
		if len(b) != 2 || len(b[0]) != 2 || len(b[1]) != 2 || b[0][0] != c[0] ||
			b[0][1] != c[1] || b[1][0] != c[2] || b[1][1] != c[3] {
			t.Errorf("expected two batches %v, got %v", tc, b)
		}
	})

}
