package main

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

func TestVersionConstant(t *testing.T) {
	if version != "0.2.1" {
		t.Errorf("version = %q, want %q", version, "0.2.1")
	}
}

// TestWhatsmeowPseudoVersion ensures go.mod pins a whatsmeow revision newer
// than the Feb 2026 one that WhatsApp deprecated (issue #106). We parse the
// pseudo-version's timestamp (YYYYMMDDHHMMSS) from go.mod.
func TestWhatsmeowPseudoVersion(t *testing.T) {
	data, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	re := regexp.MustCompile(`go\.mau\.fi/whatsmeow\s+v[\d.]+-(\d{14})-[0-9a-f]+`)
	m := re.FindStringSubmatch(string(data))
	if m == nil {
		t.Fatalf("whatsmeow pseudo-version not found in go.mod")
	}
	ts, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		t.Fatalf("parse timestamp: %v", err)
	}
	const minTS int64 = 20260327000000
	if ts < minTS {
		t.Errorf("whatsmeow pseudo-version timestamp %d < %d (outdated; run `go get -u go.mau.fi/whatsmeow@latest`)", ts, minTS)
	}
}
