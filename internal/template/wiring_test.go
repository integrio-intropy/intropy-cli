package template

import (
	"path/filepath"
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
