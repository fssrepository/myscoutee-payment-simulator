package simulator

import (
	"strings"
	"testing"
)

func TestRegistrationGeneratorFillsEveryCardField(t *testing.T) {
	document := embeddedText(t, "web/register.html")
	script := embeddedText(t, "web/register.js")

	if !strings.Contains(document, `id="generate-card-button"`) {
		t.Fatal("registration page has no header-level test-card generator")
	}
	if strings.Contains(document, `id="test-cards"`) {
		t.Fatal("registration page still renders the old in-form test-card selector")
	}
	for _, assignment := range []string{
		"cardholderInput.value =",
		"numberInput.value =",
		"monthSelect.value =",
		"yearSelect.value =",
		"securityCodeInput.value =",
	} {
		if !strings.Contains(script, assignment) {
			t.Fatalf("test-card generator does not populate %q", assignment)
		}
	}
	for _, providerCard := range []string{"4242424242424242", "5555555555554444"} {
		if !strings.Contains(script, providerCard) {
			t.Fatalf("test-card generator is missing provider card %s", providerCard)
		}
	}
	for _, marker := range []string{"awaiting3ds", "Waiting for 3DS approval", "threeDsExpiresAt"} {
		if !strings.Contains(script, marker) {
			t.Fatalf("registration UI is missing 3DS waiting marker %q", marker)
		}
	}
}

func TestProviderBrandingAcrossSimulatorSurfaces(t *testing.T) {
	for _, asset := range []string{"web/stripe.svg", "web/barion.svg"} {
		if content := embeddedText(t, asset); !strings.Contains(content, "<svg") {
			t.Fatalf("provider logo %s is not an SVG", asset)
		}
	}

	configurationDocument := embeddedText(t, "web/config.html")
	if strings.Contains(configurationDocument, "cash-only.svg") {
		t.Fatal("configuration page still uses the obsolete Cash Only icon asset")
	}
	if strings.Count(configurationDocument, ` cash-only-wordmark"`) != 2 ||
		strings.Count(configurationDocument, ">Cash Only</span>") != 2 {
		t.Fatal("configuration page must show the text-only Cash Only wordmark in the header and provider list")
	}
	if strings.Contains(configurationDocument, "TEST ONLY") || strings.Contains(configurationDocument, "test-badge") {
		t.Fatal("configuration page still renders the obsolete TEST ONLY badge")
	}
	configurationStyles := embeddedText(t, "web/config.css")
	if !strings.Contains(configurationStyles, ".provider-wordmark[hidden]") {
		t.Fatal("configuration page does not preserve exclusive active-provider visibility")
	}
	authorizationsDocument := embeddedText(t, "web/authorizations.html")
	if strings.Contains(authorizationsDocument, "TEST ONLY") || strings.Contains(authorizationsDocument, "test-badge") {
		t.Fatal("3DS confirmations page still renders the obsolete TEST ONLY badge")
	}
	authorizationsScript := embeddedText(t, "web/authorizations.js")
	for _, popupFeature := range []string{
		"popup=yes",
		"width=${width}",
		"height=${height}",
	} {
		if !strings.Contains(authorizationsScript, popupFeature) {
			t.Fatalf("3DS confirmation action is missing popup feature %q", popupFeature)
		}
	}
	confirmationScript := embeddedText(t, "web/confirmation.js")
	if !strings.Contains(confirmationScript, "window.close()") ||
		!strings.Contains(paymentMethodRegistrationAuthorizationTemplate.Tree.Root.String(), "close-confirmation") {
		t.Fatal("card registration confirmation has no working Close action")
	}

	for _, surface := range []string{
		embeddedText(t, "web/register.html"),
		embeddedText(t, "web/config.html"),
		embeddedText(t, "web/authorizations.html"),
		checkoutTemplate.Tree.Root.String(),
		bankAuthTemplate.Tree.Root.String(),
		barionGatewayTemplate.Tree.Root.String(),
		barionBankAuthTemplate.Tree.Root.String(),
		paymentWaitTemplate.Tree.Root.String(),
		paymentMethodRegistrationAuthorizationTemplate.Tree.Root.String(),
	} {
		if !strings.Contains(surface, "provider-logo") {
			t.Fatal("payment simulator surface has no provider logo")
		}
	}
}

func embeddedText(t *testing.T, name string) string {
	t.Helper()
	content, err := paymentMethodUI.ReadFile(name)
	if err != nil {
		t.Fatalf("read embedded UI asset %s: %v", name, err)
	}
	return string(content)
}
