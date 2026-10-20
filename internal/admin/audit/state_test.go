package audit

import (
	"bytes"
	"testing"
)

func TestEncodeStatePreservesNullAndRejectsInvalidJSONValues(t *testing.T) {
	t.Parallel()

	none, err := encodeState(nil)
	if err != nil {
		t.Fatalf("encode nil state: %v", err)
	}
	if none != nil {
		t.Errorf("encoded nil state = %q, want SQL NULL", none)
	}

	state, err := encodeState(State{FieldLines: 2})
	if err != nil {
		t.Fatalf("encode valid state: %v", err)
	}
	if !bytes.Equal(state, []byte(`{"lines":2}`)) {
		t.Errorf("encoded state = %s, want preserved JSON object", state)
	}

	if _, err := encodeState(State{FieldLines: make(chan int)}); err == nil {
		t.Fatal("encoding an unsupported audit value succeeded; want an error")
	}
	// Another package can still write Field{}; it has no key to store.
	if _, err := encodeState(State{Field{}: 2}); err == nil {
		t.Fatal("encoding a field with no key succeeded; want an error")
	}
}
