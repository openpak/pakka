package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDotEnvLoads(t *testing.T) {
	dir := t.TempDir()
	body := "# comment\n\n" +
		"PAKKA_TEST_FOO=bar\n" +
		"PAKKA_TEST_QUOTED=\"a b\"\n" +
		"PAKKA_TEST_SINGLE='x'\n" +
		"export PAKKA_TEST_EXPORTED=1\n" +
		"PAKKA_TEST_EMPTY=\n" +
		"bogus line here\n" +
		"PAKKA_TEST_BAD\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PAKKA_TEST_FOO", "preset")
	loadDotEnv(dir)
	if got := os.Getenv("PAKKA_TEST_FOO"); got != "preset" {
		t.Errorf("preset var overwritten: %q", got)
	}
	for key, want := range map[string]string{
		"PAKKA_TEST_QUOTED":   "a b",
		"PAKKA_TEST_SINGLE":   "x",
		"PAKKA_TEST_EXPORTED": "1",
		"PAKKA_TEST_EMPTY":    "",
	} {
		if got := os.Getenv(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if _, ok := os.LookupEnv("bogus"); ok {
		t.Error("malformed line was loaded")
	}
}

func TestDotEnvMissing(t *testing.T) {
	loadDotEnv(t.TempDir()) // must not fail
}
