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
			{map[string]any{}, recordPath + ": values.publishes is missing"},
			{map[string]any{"publishes": float64(7)}, recordPath + ": values.publishes has type float64, expected string"},
			{map[string]any{"publishes": ""}, recordPath + ": values.publishes is empty"},
		}
		for _, tc := range cases {
			if _, err := RecordValue(wiringEntry(tc.values), KeyPublishes); err == nil || filepath.ToSlash(err.Error()) != tc.wantErr {
				t.Errorf("RecordValue(%v) err = %v, want %q", tc.values, err, tc.wantErr)
			}
		}
	})

	t.Run("present value returns", func(t *testing.T) {
		got, err := RecordValue(wiringEntry(map[string]any{"publishes": "orders"}), KeyPublishes)
		if err != nil || got != "orders" {
			t.Errorf("RecordValue = %q, %v; want orders, nil", got, err)
		}
	})
}

func TestSoftValue(t *testing.T) {
	cases := []struct {
		values map[string]any
		want   string
		ok     bool
	}{
		{map[string]any{"publishes": "orders"}, "orders", true},
		{map[string]any{}, "", false},
		{map[string]any{"publishes": ""}, "", false},
		{map[string]any{"publishes": 7}, "", false},
	}
	for _, tc := range cases {
		got, ok := SoftValue(tc.values, KeyPublishes)
		if got != tc.want || ok != tc.ok {
			t.Errorf("SoftValue(%v) = %q, %v; want %q, %v", tc.values, got, ok, tc.want, tc.ok)
		}
	}
}

func TestReadScalarMessageWiring(t *testing.T) {
	pub, err := ReadPublishesMessage(wiringEntry(map[string]any{KeyPublishes: "order-created"}))
	if err != nil || pub != "order-created" {
		t.Fatalf("ReadPublishesMessage = %q, %v", pub, err)
	}

	sub, err := ReadSubscribesMessage(wiringEntry(map[string]any{KeySubscribes: "order-created"}))
	if err != nil || sub != "order-created" {
		t.Fatalf("ReadSubscribesMessage = %q, %v", sub, err)
	}
}

func TestMessageValue(t *testing.T) {
	if got := MessageValue("order-created"); got != "order-created" {
		t.Errorf("MessageValue = %q", got)
	}
}
