package main

import "testing"

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
