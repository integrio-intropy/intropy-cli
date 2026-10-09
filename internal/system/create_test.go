package system

import (
	"context"
	"strings"
	"testing"
)

func TestCreateRequiresName(t *testing.T) {
	err := Create(context.Background(), CreateOptions{})
	if err == nil || !strings.Contains(err.Error(), "system name is required") {
		t.Fatalf("err = %v", err)
	}
}
