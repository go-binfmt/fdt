package fdt

import (
	"errors"
	"testing"
)

// Find takes absolute paths only; Node.Lookup is the relative form.
func TestFindRequiresAbsolutePath(t *testing.T) {
	tr := New()
	if _, err := tr.Root.AddChild("images"); err != nil {
		t.Fatal(err)
	}
	if n, err := tr.Find("/images"); err != nil || n.Name != "images" {
		t.Fatalf("absolute: %v %v", n, err)
	}
	if n, err := tr.Find("images"); !errors.Is(err, ErrNotFound) || n != nil {
		t.Errorf("relative path accepted: %v %v", n, err)
	}
	if n, err := tr.Root.Lookup("images"); err != nil || n.Name != "images" {
		t.Errorf("Lookup relative: %v %v", n, err)
	}
}
