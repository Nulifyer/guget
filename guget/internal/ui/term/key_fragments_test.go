package term

import (
	"bytes"
	"io"
	"testing"
)

func TestEscapeTimeoutPreservesLaterInput(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	gate := make(chan struct{})
	go func() {
		defer writer.Close()
		_, _ = writer.Write([]byte{0x1b})
		<-gate
		_, _ = writer.Write([]byte("ab"))
	}()
	r := NewKeyReader(reader)
	got, err := r.Read()
	close(gate)
	if err != nil || got.(Key).Kind != KeyEsc {
		t.Fatalf("timeout: %+v, %v", got, err)
	}
	for _, want := range "ab" {
		got, err = r.Read()
		if err != nil || got.(Key).Rune != want {
			t.Fatalf("after timeout: %+v, %v; want %q", got, err, want)
		}
	}
}

type fragmentedKeyReader struct{ data []byte }

func (r *fragmentedKeyReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	p[0] = r.data[0]
	r.data = r.data[1:]
	return 1, nil
}
func TestKeyReaderFragmentedArrowMustDecodeAsArrow(t *testing.T) {
	r := NewKeyReader(&fragmentedKeyReader{data: []byte("\x1b[A")})
	got, err := r.Read()
	if err != nil {
		t.Fatal(err)
	}
	k, ok := got.(Key)
	if !ok || k.Kind != KeyUp {
		t.Fatalf("one-byte read chunks decoded arrow as %+v", got)
	}
}
func TestKeyReaderInvalidUTF8MustNotConsumeFollowingASCII(t *testing.T) {
	r := NewKeyReader(bytes.NewReader([]byte{0xc2, 'A', 'b'}))
	first, _ := r.Read()
	second, _ := r.Read()
	k, ok := second.(Key)
	if !ok || k.Rune != 'A' {
		t.Fatalf("invalid lead consumed valid ASCII: first=%+v next=%+v", first, second)
	}
}
