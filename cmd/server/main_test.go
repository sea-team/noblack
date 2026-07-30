package main

import (
	"flag"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestServerUsesDataWordDatabaseByDefault(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=TestServerDefaultWordsHelper")
	command.Dir = t.TempDir()
	command.Env = append(os.Environ(), "NOBLACK_SERVER_DEFAULT_WORDS_HELPER=1")

	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("server unexpectedly started without a word database")
	}
	if !strings.Contains(string(output), "data/words.json") {
		t.Fatalf("server output = %q, want data/words.json", output)
	}
}

func TestServerDefaultWordsHelper(t *testing.T) {
	if os.Getenv("NOBLACK_SERVER_DEFAULT_WORDS_HELPER") != "1" {
		return
	}
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	os.Args = []string{os.Args[0]}
	main()
	os.Exit(0)
}

func TestModelServiceURLDefaultsToDisabled(t *testing.T) {
	t.Setenv("NB_MODEL_SERVICE_URL", "")
	if got := configuredModelServiceURL(); got != "" {
		t.Fatalf("configuredModelServiceURL() = %q, want disabled empty URL", got)
	}
}

func TestModelServiceURLPreservesExplicitURL(t *testing.T) {
	t.Setenv("NB_MODEL_SERVICE_URL", "http://127.0.0.1:8091")
	if got := configuredModelServiceURL(); got != "http://127.0.0.1:8091" {
		t.Fatalf("configuredModelServiceURL() = %q, want explicit URL", got)
	}
}
