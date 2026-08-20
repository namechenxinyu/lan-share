package fileutil

import "testing"

func TestCleanFileName(t *testing.T) {
	name, err := CleanFileName(`../../folder\\hello.bin`)
	if err != nil {
		t.Fatal(err)
	}
	if name != "hello.bin" {
		t.Fatalf("got %q", name)
	}
}

func TestContentDisposition(t *testing.T) {
	got := ContentDisposition("测试 文件.zip")
	if got == "" {
		t.Fatal("empty content disposition")
	}
}
