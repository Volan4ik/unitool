package admin

import "testing"

func TestValidatePackageInputRejectsTextAttempts(t *testing.T) {
	_, err := validatePackageInput(PackageInput{
		Code:          "legacy",
		Name:          "Legacy",
		PriceRub:      100,
		Currency:      "RUB",
		AttemptsText:  10,
		AttemptsImage: 0,
		AttemptsVideo: 0,
		IsActive:      true,
	})
	if err == nil {
		t.Fatal("expected error for deprecated text attempts")
	}
}

func TestValidatePackageInputRequiresImageOrVideo(t *testing.T) {
	_, err := validatePackageInput(PackageInput{
		Code:          "empty",
		Name:          "Empty",
		PriceRub:      100,
		Currency:      "RUB",
		AttemptsText:  0,
		AttemptsImage: 0,
		AttemptsVideo: 0,
		IsActive:      true,
	})
	if err == nil {
		t.Fatal("expected error when image/video attempts are both zero")
	}
}

func TestValidatePackageInputAcceptsImageVideoPackage(t *testing.T) {
	_, err := validatePackageInput(PackageInput{
		Code:          "mix_30_5",
		Name:          "Комбо S",
		PriceRub:      619,
		Currency:      "RUB",
		AttemptsText:  0,
		AttemptsImage: 30,
		AttemptsVideo: 5,
		IsActive:      true,
	})
	if err != nil {
		t.Fatalf("expected package to be valid, got err=%v", err)
	}
}
