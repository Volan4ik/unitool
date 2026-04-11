package admin

import "testing"

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
