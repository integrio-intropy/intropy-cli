package xregistry

import (
	"errors"
	"strings"
	"testing"
)

func TestSplitChannel(t *testing.T) {
	cases := []struct {
		in     string
		pubsub string
		topic  string
		wantOk bool
	}{
		{"product-pubsub/product-topic", "product-pubsub", "product-topic", true},
		{"product-pubsub/deep/with/slashes", "product-pubsub", "deep/with/slashes", true},
		{"no-slash", "", "", false},
		{"/empty-pubsub", "", "", false},
		{"empty-topic/", "", "", false},
		{"", "", "", false},
	}
	for _, tc := range cases {
		pubsub, topic, err := SplitChannel(tc.in)
		if gotOk := err == nil; gotOk != tc.wantOk {
			t.Errorf("SplitChannel(%q) ok = %v, want %v (err %v)", tc.in, gotOk, tc.wantOk, err)
			continue
		}
		if tc.wantOk && (pubsub != tc.pubsub || topic != tc.topic) {
			t.Errorf("SplitChannel(%q) = %q, %q, want %q, %q", tc.in, pubsub, topic, tc.pubsub, tc.topic)
		}
	}
}

// Two groups carrying the same message id: a bare reference names both, so
// looking it up is refused rather than guessed.
func TestFindMessageBareRefAmbiguousAcrossGroups(t *testing.T) {
	doc := &Export{
		MessageGroups: map[string]MessageGroup{
			"one": {ID: "one", Messages: map[string]Message{"shared.event": {ID: "shared.event"}}},
			"two": {ID: "two", Messages: map[string]Message{"shared.event": {ID: "shared.event"}}},
		},
	}
	_, _, err := FindMessage(doc, "shared.event")
	if err == nil || !strings.Contains(err.Error(), "/messagegroups/one") || !strings.Contains(err.Error(), "/messagegroups/two") {
		t.Errorf("err = %v, want ambiguity naming both groups", err)
	}
}

func TestFindMessageEmptyExportDegradesToEmptyMaps(t *testing.T) {
	// An export with no collections at all is a valid empty registry.
	doc := &Export{}
	_, _, err := FindMessage(doc, "anything")
	var notFound *MessageNotFoundError
	if !errors.As(err, &notFound) {
		t.Errorf("err = %v, want MessageNotFoundError on the empty registry", err)
	}
}

func TestDefaultSchemaVersionXIDPinsTheDefaultVersion(t *testing.T) {
	doc := &Export{
		SchemaGroups: map[string]SchemaGroup{
			"sg": {ID: "sg", Schemas: map[string]Schema{
				"s": {ID: "s", Versions: map[string]Version{
					"3": {VersionID: "3", XID: "/schemagroups/sg/schemas/s/versions/3", IsDefault: true},
					"2": {VersionID: "2", XID: "/schemagroups/sg/schemas/s/versions/2"},
				}},
			}},
		},
	}
	xid, err := DefaultSchemaVersionXID(doc, "/schemagroups/sg/schemas/s")
	if err != nil {
		t.Fatal(err)
	}
	if xid != "/schemagroups/sg/schemas/s/versions/3" {
		t.Errorf("xid = %q, want the default version's xid", xid)
	}

	for _, bad := range []string{"not-an-xid", "/schemagroups/sg/schemas/unknown", "/schemagroups/unknown/schemas/s"} {
		if _, err := DefaultSchemaVersionXID(doc, bad); err == nil {
			t.Errorf("DefaultSchemaVersionXID(%q) err = nil, want SchemaNotFoundError", bad)
		} else {
			var notFound *SchemaNotFoundError
			if !errors.As(err, &notFound) {
				t.Errorf("DefaultSchemaVersionXID(%q) err = %v, want SchemaNotFoundError", bad, err)
			}
		}
	}
}
