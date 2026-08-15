package sensei

import (
	"bufio"
	"bytes"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	var b bytes.Buffer
	if err := writeFrame(&b, map[string]any{"x": "y"}); err != nil {
		t.Fatal(err)
	}
	body, err := readFrame(bufio.NewReader(&b))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"x":"y"}` {
		t.Fatalf("unexpected body %s", body)
	}
}
