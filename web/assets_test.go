package web_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func TestDuckIconIsPinnedSafeNotoSVG(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("assets/src/duck-icon.svg")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	const expected = "dada0715179ef9cfc6467993c37509b312ad2c68f0b1e958885e1c2f2dc1c7a1"
	digest := sha256.Sum256(data)
	if actual := hex.EncodeToString(digest[:]); actual != expected {
		t.Fatalf("SVG checksum = %s, want pinned Noto asset %s", actual, expected)
	}

	lower := strings.ToLower(string(data))
	for _, forbidden := range []string{"<script", "<image", "<foreignobject", "<iframe", "<object", "<use", "href=", "onload=", "javascript:"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("SVG contains forbidden content %q", forbidden)
		}
	}

	decoder := xml.NewDecoder(bytes.NewReader(data))
	paths := 0
	for {
		token, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decode SVG: %v", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local == "path" {
			paths++
		}
		for _, attribute := range start.Attr {
			name := strings.ToLower(attribute.Name.Local)
			if name == "href" || strings.HasPrefix(name, "on") || strings.Contains(strings.ToLower(attribute.Value), "javascript:") {
				t.Fatalf("unsafe SVG attribute %q", attribute.Name.Local)
			}
		}
	}
	if paths == 0 {
		t.Fatal("SVG has no paths")
	}
}
