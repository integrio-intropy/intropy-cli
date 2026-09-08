package template

import (
	"path/filepath"
	"strings"
	"testing"
)

func wiringEntry(values map[string]any) ScaffoldEntry {
	return ScaffoldEntry{Path: "order-extractor", Scaffold: Scaffold{Values: values}}
}

func TestRecordValue(t *testing.T) {
	recordPath := filepath.ToSlash(filepath.Join("order-extractor", filepath.FromSlash(ScaffoldRelPath)))

	t.Run("missing, mistyped, and empty name the record", func(t *testing.T) {
		cases := []struct {
			values  map[string]any
			wantErr string
		}{
			{map[string]any{}, recordPath + ": values.topic is missing"},
			{map[string]any{"topic": float64(7)}, recordPath + ": values.topic has type float64, expected string"},
			{map[string]any{"topic": ""}, recordPath + ": values.topic is empty"},
		}
		for _, tc := range cases {
			if _, err := RecordValue(wiringEntry(tc.values), KeyTopic); err == nil || filepath.ToSlash(err.Error()) != tc.wantErr {
				t.Errorf("RecordValue(%v) err = %v, want %q", tc.values, err, tc.wantErr)
			}
		}
	})

	t.Run("present value returns", func(t *testing.T) {
		got, err := RecordValue(wiringEntry(map[string]any{"topic": "orders"}), KeyTopic)
		if err != nil || got != "orders" {
			t.Errorf("RecordValue = %q, %v; want orders, nil", got, err)
		}
	})
}

func TestRecordValueDefault(t *testing.T) {
	t.Run("missing falls back", func(t *testing.T) {
		got, err := RecordValueDefault(wiringEntry(map[string]any{}), KeyPubsub, DefaultPubsub)
		if err != nil || got != DefaultPubsub {
			t.Errorf("RecordValueDefault = %q, %v; want %q, nil", got, err, DefaultPubsub)
		}
	})

	t.Run("present but mistyped or empty is an error, not the fallback", func(t *testing.T) {
		for _, values := range []map[string]any{{"pubsub": 7}, {"pubsub": ""}} {
			if _, err := RecordValueDefault(wiringEntry(values), KeyPubsub, DefaultPubsub); err == nil {
				t.Errorf("RecordValueDefault(%v) = nil error, want a record error", values)
			}
		}
	})
}

func TestSoftValue(t *testing.T) {
	cases := []struct {
		values map[string]any
		want   string
		ok     bool
	}{
		{map[string]any{"topic": "orders"}, "orders", true},
		{map[string]any{}, "", false},
		{map[string]any{"topic": ""}, "", false},
		{map[string]any{"topic": 7}, "", false},
	}
	for _, tc := range cases {
		got, ok := SoftValue(tc.values, KeyTopic)
		if got != tc.want || ok != tc.ok {
			t.Errorf("SoftValue(%v) = %q, %v; want %q, %v", tc.values, got, ok, tc.want, tc.ok)
		}
	}
}

var (
	subscribeValues = map[string]any{
		"subscribe": map[string]any{
			"message":       "io.intropy.maxbo.product.export",
			"pubsub":        "product-distribution-pubsub",
			"topic":         "sbt-test-product-extractor-001",
			"dataschema":    "/schemagroups/io.intropy.maxbo.product/schemas/product-export.v1",
			"dataschemaurl": "https://registry.intropy.io/schemagroups/io.intropy.maxbo.product/schemas/product-export.v1/versions/1",
		},
	}
	publishesValues = map[string]any{
		"publishes": map[string]any{
			"message":    "product-exported",
			"contract":   "ProductExported",
			"dataschema": "/schemagroups/g/schemas/product-exported.v1",
		},
	}
)

func TestReadSubscribeBlock(t *testing.T) {
	record := filepath.ToSlash(filepath.Join("order-extractor", filepath.FromSlash(ScaffoldRelPath)))

	t.Run("full snapshot reads every field", func(t *testing.T) {
		b, err := ReadSubscribeBlock(wiringEntry(subscribeValues))
		if err != nil {
			t.Fatal(err)
		}
		want := SubscribeBlock{
			Message:       "io.intropy.maxbo.product.export",
			Pubsub:        "product-distribution-pubsub",
			Topic:         "sbt-test-product-extractor-001",
			Dataschema:    "/schemagroups/io.intropy.maxbo.product/schemas/product-export.v1",
			DataschemaURL: "https://registry.intropy.io/schemagroups/io.intropy.maxbo.product/schemas/product-export.v1/versions/1",
		}
		if *b != want {
			t.Errorf("block = %+v, want %+v", *b, want)
		}
		if !b.External() {
			t.Error("a snapshot block is external")
		}
	})

	t.Run("internal block carries only the message", func(t *testing.T) {
		b, err := ReadSubscribeBlock(wiringEntry(map[string]any{
			"subscribe": map[string]any{"message": "product-exported"},
		}))
		if err != nil {
			t.Fatal(err)
		}
		if b.Message != "product-exported" || b.Topic != "" || b.Pubsub != "" {
			t.Errorf("block = %+v", b)
		}
		if b.External() {
			t.Error("a message-only block is internal")
		}
	})

	t.Run("absent block reads nil, not an error", func(t *testing.T) {
		b, err := ReadSubscribeBlock(wiringEntry(map[string]any{"topic": "orders"}))
		if err != nil || b != nil {
			t.Errorf("ReadSubscribeBlock = %v, %v; want nil, nil", b, err)
		}
	})

	t.Run("mistyped snapshot field names the record and the key", func(t *testing.T) {
		values := map[string]any{
			"subscribe": map[string]any{"message": "m", "pubsub": float64(7)},
		}
		err := readError(values)
		if err == nil || !strings.Contains(err.Error(), record) || !strings.Contains(err.Error(), "subscribe.pubsub") {
			t.Errorf("err = %v, want an error naming %s and subscribe.pubsub", err, record)
		}
	})

	t.Run("empty message names the record", func(t *testing.T) {
		values := map[string]any{
			"subscribe": map[string]any{"message": ""},
		}
		err := readError(values)
		if err == nil || !strings.Contains(err.Error(), record) || !strings.Contains(err.Error(), "message") {
			t.Errorf("err = %v, want an error naming the record and message", err)
		}
	})

	t.Run("missing message names the record", func(t *testing.T) {
		err := readError(map[string]any{"subscribe": map[string]any{"pubsub": "p"}})
		if err == nil || !strings.Contains(err.Error(), record) {
			t.Errorf("err = %v, want an error naming the record", err)
		}
	})

	t.Run("mistyped block names the record and the block", func(t *testing.T) {
		err := readError(map[string]any{"subscribe": "orders"})
		if err == nil || !strings.Contains(err.Error(), "expected object") {
			t.Errorf("err = %v, want a block-type error", err)
		}
	})
}

func TestReadPublishesBlock(t *testing.T) {
	t.Run("message and contract read", func(t *testing.T) {
		b, err := ReadPublishesBlock(wiringEntry(publishesValues))
		if err != nil {
			t.Fatal(err)
		}
		if b.Message != "product-exported" || b.Contract != "ProductExported" || b.Dataschema != "/schemagroups/g/schemas/product-exported.v1" {
			t.Errorf("block = %+v", b)
		}
	})
	t.Run("contract is optional", func(t *testing.T) {
		b, err := ReadPublishesBlock(wiringEntry(map[string]any{
			"publishes": map[string]any{"message": "product-exported"},
		}))
		if err != nil {
			t.Fatal(err)
		}
		if b.Contract != "" {
			t.Errorf("contract = %q, want empty (optional during transition)", b.Contract)
		}
	})
	t.Run("absent block reads nil", func(t *testing.T) {
		b, err := ReadPublishesBlock(wiringEntry(map[string]any{}))
		if err != nil || b != nil {
			t.Errorf("ReadPublishesBlock = %v, %v; want nil, nil", b, err)
		}
	})
	t.Run("empty message is an error", func(t *testing.T) {
		if _, err := ReadPublishesBlock(wiringEntry(map[string]any{"publishes": map[string]any{"message": ""}})); err == nil {
			t.Error("empty message should be an error")
		}
	})
}

func TestBlockWinsOverLegacyKeys(t *testing.T) {
	values := map[string]any{
		"topic":    "legacy-topic",
		"contract": "LegacyContract",
		"pubsub":   "legacy-pubsub",
		"publishes": map[string]any{
			"message": "product-exported",
		},
	}
	if !HasMessageBlocks(values) || !HasPublishesValue(values) {
		t.Fatal("HasMessageBlocks should see the publishes block")
	}
	pub, err := ReadPublishesBlock(wiringEntry(values))
	if err != nil || pub.Message != "product-exported" {
		t.Fatalf("ReadPublishesBlock = %+v, %v", pub, err)
	}
	// The legacy reader is untouched: nothing turned the flat keys into an
	// error, so old records and old parsers keep reading them.
	topic, err := RecordValue(wiringEntry(values), KeyTopic)
	if err != nil || topic != "legacy-topic" {
		t.Errorf("RecordValue(topic) = %q, %v; the legacy path must keep working", topic, err)
	}
}

func TestSubscribeBlockValueEmitsNoLegacyKeys(t *testing.T) {
	v := SubscribeBlockValue(&SubscribeBlock{Message: "m", Pubsub: "p", Topic: "t"})
	for _, legacy := range []string{KeyContract} {
		if _, ok := v[legacy]; ok {
			t.Errorf("writers must not emit legacy key %q", legacy)
		}
	}
}

func TestPublishesBlockValueEmitsOptionalDataschema(t *testing.T) {
	v := PublishesBlockValue(&PublishesBlock{Message: "m", Contract: "C", Dataschema: "/schemas/m"})
	if v[KeyMessage] != "m" || v[KeyContract] != "C" || v[KeyDataschema] != "/schemas/m" {
		t.Errorf("publishes value = %#v", v)
	}
}

func TestPublishesBlockSnapshotRoundTrip(t *testing.T) {
	full := map[string]any{
		"publishes": map[string]any{
			"message":       "io.intropy.maxbo.product.export",
			"pubsub":        "product-distribution-pubsub",
			"topic":         "sbt-test-product-extractor-001",
			"dataschema":    "/schemagroups/g/schemas/product-export.v1",
			"dataschemaurl": "https://registry.example/schemagroups/g/schemas/product-export.v1/versions/1",
		},
	}
	b, err := ReadPublishesBlock(wiringEntry(full))
	if err != nil {
		t.Fatal(err)
	}
	if !b.External() {
		t.Error("a topic-carrying publishes block must be external")
	}
	if b.Pubsub != "product-distribution-pubsub" || b.Topic != "sbt-test-product-extractor-001" ||
		b.DataschemaURL != "https://registry.example/schemagroups/g/schemas/product-export.v1/versions/1" {
		t.Errorf("round trip lost fields: %+v", b)
	}
	// The writer emits only non-empty fields, so the read-back stays
	// identical to the written shape.
	back, err := ReadPublishesBlock(wiringEntry(map[string]any{"publishes": PublishesBlockValue(b)}))
	if err != nil {
		t.Fatal(err)
	}
	if *back != *b {
		t.Errorf("value round trip = %+v, want %+v", back, b)
	}
}

func TestPublishesBlockInternalShape(t *testing.T) {
	b, err := ReadPublishesBlock(wiringEntry(map[string]any{
		"publishes": map[string]any{"message": "product-exported"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if b.External() {
		t.Error("a topic-less publishes block must not be external")
	}
	// A topic without pubsub reads successfully here: assembly validates
	// the pair (it names the record either way), the block reader stays a
	// shape reader.
	b, err = ReadPublishesBlock(wiringEntry(map[string]any{
		"publishes": map[string]any{"message": "m", "topic": "t"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !b.External() {
		t.Error("a block carrying a topic is external regardless of pubsub")
	}
}

func TestPublishesBlockStrictSnapshotFields(t *testing.T) {
	for name, block := range map[string]map[string]any{
		"mistyped topic":         {"message": "m", "topic": 7},
		"empty dataschemaurl":    {"message": "m", "dataschemaurl": ""},
		"mistyped dataschemaurl": {"message": "m", "dataschemaurl": true},
	} {
		t.Run(name, func(t *testing.T) {
			err := func() error {
				_, err := ReadPublishesBlock(wiringEntry(map[string]any{"publishes": block}))
				return err
			}()
			if err == nil || !strings.Contains(err.Error(), "values.publishes.") {
				t.Errorf("err = %v, want a strict error naming the block and key path", err)
			}
		})
	}
}

// readError runs ReadSubscribeBlock and returns its error, so each case
// above can assert on messages without duplicating the call.
func readError(values map[string]any) error {
	_, err := ReadSubscribeBlock(wiringEntry(values))
	return err
}
