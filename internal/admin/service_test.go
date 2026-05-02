package admin

import (
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "unitool/internal/db/generated"
)

func TestValidatePackageInput(t *testing.T) {
	in := PackageInput{
		Code:          " BASE_MINIMUM ",
		Name:          " Base ",
		PriceRub:      690,
		Currency:      "rub",
		AttemptsText:  0,
		AttemptsImage: 30,
		AttemptsVideo: 5,
		IsActive:      true,
	}
	got, err := validatePackageInput(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Code != "base_minimum" || got.Title != "Base" || got.Currency != "RUB" {
		t.Fatalf("unexpected normalized params: %+v", got)
	}

	cases := []PackageInput{
		{Code: "", Name: "x", Currency: "RUB", AttemptsImage: 1},                                 // missing code
		{Code: "x", Name: "", Currency: "RUB", AttemptsImage: 1},                                 // missing name
		{Code: "x", Name: "y", Currency: "RUB", AttemptsImage: -1},                               // negative
		{Code: "x", Name: "y", Currency: "RUB", AttemptsText: 1, AttemptsImage: 1},               // text attempts
		{Code: "x", Name: "y", Currency: "RUB", AttemptsImage: 0, AttemptsVideo: 0},              // empty package
		{Code: "x", Name: "y", Currency: "RU", AttemptsImage: 1},                                 // currency len
		{Code: "x", Name: "y", Currency: "USD", AttemptsImage: 1},                                // unsupported currency
		{Code: "x", Name: "y", Currency: "", AttemptsImage: 0, AttemptsVideo: 0},                 // default currency but empty attempts
		{Code: "x", Name: "y", Currency: "  ", AttemptsImage: 1, AttemptsVideo: 0, PriceRub: -1}, // negative price
	}
	for i, tc := range cases {
		if _, err := validatePackageInput(tc); err == nil {
			t.Fatalf("case #%d expected error", i)
		}
	}
}

func TestBuildUsersCSV(t *testing.T) {
	createdAt := time.Date(2026, 4, 11, 10, 20, 30, 0, time.UTC)
	data, err := buildUsersCSV([]db.ListUsersForExportRow{
		{
			ID:               1,
			TgID:             123,
			Username:         pgtype.Text{String: "user", Valid: true},
			FirstName:        pgtype.Text{String: "First, quoted", Valid: true},
			IsBanned:         true,
			BannedReason:     pgtype.Text{String: "spam", Valid: true},
			ImageBalance:     5,
			VideoBalance:     2,
			CreatedAt:        pgtype.Timestamptz{Time: createdAt, Valid: true},
			UpdatedAt:        pgtype.Timestamptz{Time: createdAt, Valid: true},
			SourceTag:        "ads",
			GenerationCount:  9,
			PaidOrdersCount:  1,
			PaidAmountRub:    690,
			LastPackageCode:  pgtype.Text{String: "base", Valid: true},
			LastPackageTitle: pgtype.Text{String: "Base", Valid: true},
			LastPaidAt:       pgtype.Timestamptz{Time: createdAt, Valid: true},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(string(data), "\xEF\xBB\xBF") {
		t.Fatal("csv must include UTF-8 BOM for spreadsheet compatibility")
	}

	r := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(data), "\xEF\xBB\xBF")))
	records, err := r.ReadAll()
	if err != nil {
		t.Fatalf("csv must parse: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records=%d want=2", len(records))
	}
	if records[0][1] != "telegram_id" || records[1][1] != "123" || records[1][3] != "First, quoted" {
		t.Fatalf("unexpected records: %#v", records)
	}
}
