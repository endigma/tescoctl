package auth

import (
	"context"
	"strings"
	"testing"
)

// A syntactically valid JWT whose payload has no exp, so tests exercise the
// parser without pinning a clock.
const testToken = "aGVhZGVy.eyJzdWIiOiJ0ZXN0In0.c2ln"

func harvest(t *testing.T, input string) *Session {
	t.Helper()
	session, err := (&Importer{Src: strings.NewReader(input)}).Harvest(context.Background())
	if err != nil {
		t.Fatalf("Harvest(%.40q): %v", input, err)
	}
	return session
}

// TestImportFormats covers every shape a user can realistically paste in.
func TestImportFormats(t *testing.T) {
	cases := map[string]string{
		"extension JSON array": `[
			{"name":"OAuth.AccessToken","value":"` + testToken + `","domain":".tesco.com"},
			{"name":"UUID","value":"cust-1"},
			{"name":"bm_sv","value":"akamai"},
			{"name":"unrelated","value":"drop"}
		]`,
		"name/value object": `{"OAuth.AccessToken":"` + testToken + `","UUID":"cust-1","bm_sv":"akamai"}`,
		"wrapped envelope": `{"cookies":[
			{"name":"OAuth.AccessToken","value":"` + testToken + `"},
			{"name":"UUID","value":"cust-1"},
			{"name":"bm_sv","value":"akamai"}
		]}`,
		"raw cookie header":           `OAuth.AccessToken=` + testToken + `; UUID=cust-1; bm_sv=akamai; unrelated=drop`,
		"labelled cookie header":      `Cookie: OAuth.AccessToken=` + testToken + `; UUID=cust-1; bm_sv=akamai`,
		"cookie header with newlines": "OAuth.AccessToken=" + testToken + ";\n  UUID=cust-1;\n  bm_sv=akamai",
	}

	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			session := harvest(t, input)
			if session.AccessToken != testToken {
				t.Errorf("AccessToken = %q, want %q", session.AccessToken, testToken)
			}
			if session.CustomerUUID != "cust-1" {
				t.Errorf("CustomerUUID = %q, want cust-1", session.CustomerUUID)
			}
			if _, kept := session.Cookies["unrelated"]; kept {
				t.Error("irrelevant cookies should be dropped")
			}
			if _, kept := session.Cookies["bm_sv"]; !kept {
				t.Error("Akamai cookies should be kept")
			}
		})
	}
}

// TestImportRejectsIncomplete is the common failure: exporting before signing
// in, or exporting from the wrong domain.
func TestImportRejectsIncomplete(t *testing.T) {
	for name, input := range map[string]string{
		"no auth cookies":   `[{"name":"bm_sv","value":"akamai"}]`,
		"token but no uuid": `OAuth.AccessToken=` + testToken,
		"empty":             "   ",
		"not cookies":       `<html>nope</html>`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := (&Importer{Src: strings.NewReader(input)}).Harvest(context.Background())
			if err == nil {
				t.Fatal("want an error, got nil")
			}
		})
	}
}
