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
// resolving it is refused rather than guessed.
func TestResolveBareRefAmbiguousAcrossGroups(t *testing.T) {
	doc := &Export{
		MessageGroups: map[string]MessageGroup{
			"one": {ID: "one", Messages: map[string]Message{"shared.event": {ID: "shared.event"}}},
			"two": {ID: "two", Messages: map[string]Message{"shared.event": {ID: "shared.event"}}},
		},
	}
	_, err := ResolveMessage(doc, "shared.event")
	if err == nil || !strings.Contains(err.Error(), "/messagegroups/one") || !strings.Contains(err.Error(), "/messagegroups/two") {
		t.Errorf("err = %v, want ambiguity naming both groups", err)
	}
}

func TestResolveListOfExportDegradesToEmptyMaps(t *testing.T) {
	// An export with no collections at all is a valid empty registry.
	doc := &Export{}
	_, err := ResolveMessage(doc, "anything")
	var notFound *MessageNotFoundError
	if !errors.As(err, &notFound) {
		t.Errorf("err = %v, want MessageNotFoundError on the empty registry", err)
	}
}

func TestResolveChannelsAreSorted(t *testing.T) {
	doc := &Export{
		MessageGroups: map[string]MessageGroup{
			"g": {ID: "g", Messages: map[string]Message{"m": {ID: "m", Versions: map[string]*MessageVersion{
				"1": {VersionID: "1", IsDefault: true},
			}}}},
		},
		Endpoints: map[string]Endpoint{
			"b": {ID: "b", Usage: []string{"producer"}, Channel: "z-pubsub/z-topic", MessageGroups: []string{"/messagegroups/g"}},
			"a": {ID: "a", Usage: []string{"producer"}, Channel: "a-pubsub/a-topic", MessageGroups: []string{"/messagegroups/g"}},
		},
	}
	res, err := ResolveMessage(doc, "m")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Channels) != 2 || res.Channels[0].Pubsub != "a-pubsub" {
		t.Errorf("channels = %+v, want sorted by pubsub", res.Channels)
	}
}

func TestSchemaDefaultVersionPinnedFromDocView(t *testing.T) {
	doc := &Export{
		MessageGroups: map[string]MessageGroup{
			"g": {ID: "g", Messages: map[string]Message{"m": {ID: "m", Versions: map[string]*MessageVersion{
				"1": {VersionID: "1", IsDefault: true, DataSchemaXID: "/schemagroups/sg/schemas/s"},
			}}}},
		},
		SchemaGroups: map[string]SchemaGroup{
			"sg": {ID: "sg", Schemas: map[string]Schema{
				"s": {ID: "s", Versions: map[string]Version{
					"3": {VersionID: "3", XID: "/schemagroups/sg/schemas/s/versions/3", IsDefault: true},
					"2": {VersionID: "2", XID: "/schemagroups/sg/schemas/s/versions/2"},
				}},
			}},
		},
	}
	res, err := ResolveMessage(doc, "m")
	if err != nil {
		t.Fatal(err)
	}
	if res.DataSchemaURL != "/schemagroups/sg/schemas/s/versions/3" {
		t.Errorf("DataSchemaURL = %q, want the default version's xid", res.DataSchemaURL)
	}
}
