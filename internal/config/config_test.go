package config

import "testing"

func TestParseAdminIDs(t *testing.T) {
	c := Config{}
	ids, err := c.ParseAdminIDs()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ids != nil {
		t.Fatalf("expected nil for empty ADMIN_IDS, got %#v", ids)
	}

	c.AdminIDs = "123, 456, ,789"
	ids, err = c.ParseAdminIDs()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 3 || ids[0] != 123 || ids[1] != 456 || ids[2] != 789 {
		t.Fatalf("unexpected ids: %#v", ids)
	}

	c.AdminIDs = "123,abc"
	if _, err := c.ParseAdminIDs(); err == nil {
		t.Fatal("expected parse error for invalid id")
	}
}
