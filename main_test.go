package main

import (
	"testing"
)

func TestFetchImageTo(t *testing.T) {
	dir := t.TempDir()
	err := FetchImageTo("library", "busybox", dir)
	if err != nil {
		t.Fatal(err)
	}
}
