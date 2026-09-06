package simulator

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

type Server struct {
	config                     Config
	now                        func() time.Time
	client                     *http.Client
	db                         *sql.DB
	mux                        *http.ServeMux
	mu                         sync.RWMutex
	sessions                   map[string]*CheckoutSession
	intents                    map[string]*PaymentIntent
	idempotency                map[string]idempotencyRecord
	events                     map[string]*WebhookEvent
	eventOrder                 []string
	barionPayments             map[string]*BarionPayment
	barionRequestIndex         map[string]barionRequestRecord
	registrations              map[string]*PaymentMethodRegistration
	configuration              SimulatorConfiguration
	generatedCardSequences     map[string]int
	configurationAccessTickets map[string]time.Time
	configurationSessions      map[string]time.Time
}

type CheckoutSession struct {
	ID                string             `json:"id"`
	Object            string             `json:"object"`
	URL               string             `json:"url"`
	Status            string             `json:"status"`
	PaymentStatus     string             `json:"payment_status"`
	Mode              string             `json:"mode"`
	AmountTotal       int64              `json:"amount_total"`
	Currency          string             `json:"currency"`
	SuccessURL        string             `json:"success_url"`
	CancelURL         string             `json:"cancel_url"`
	ClientReferenceID string             `json:"client_reference_id,omitempty"`
	Metadata          map[string]string  `json:"metadata,omitempty"`
	Created           int64              `json:"created"`
	Livemode          bool               `json:"livemode"`
	PaymentIntent     string             `json:"payment_intent"`
	LineItems         []CheckoutLineItem `json:"line_items,omitempty"`
	ControlToken      string             `json:"-"`
	IdempotencyKey    string             `json:"-"`
}

// CheckoutLineItem is the subset of Stripe Checkout line-item data that the
// simulator must preserve in order to render the same basket MyScoutee sent to
// the gateway. It deliberately contains no cardholder or payment-method data.
type CheckoutLineItem struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Quantity    int64  `json:"quantity"`
	UnitAmount  int64  `json:"unit_amount"`
	AmountTotal int64  `json:"amount_total"`
	Currency    string `json:"currency"`
}

type PaymentIntent struct {
	ID                 string                   `json:"id"`
	Object             string                   `json:"object"`
	Amount             int64                    `json:"amount"`
	AmountCapturable   int64                    `json:"amount_capturable"`
	AmountReceived     int64                    `json:"amount_received"`
	Currency           string                   `json:"currency"`
	Status             string                   `json:"status"`
	CaptureMethod      string                   `json:"capture_method"`
	PaymentMethod      string                   `json:"payment_method,omitempty"`
	ClientReferenceID  string                   `json:"client_reference_id,omitempty"`
	Metadata           map[string]string        `json:"metadata,omitempty"`
	CaptureBefore      int64                    `json:"capture_before,omitempty"`
	CancellationReason string                   `json:"cancellation_reason,omitempty"`
	NextAction         *PaymentIntentNextAction `json:"next_action,omitempty"`
	LastPaymentError   *PaymentIntentError      `json:"last_payment_error,omitempty"`
	Created            int64                    `json:"created"`
	Livemode           bool                     `json:"livemode"`
	IdempotencyKey     string                   `json:"-"`
}

type PaymentIntentNextAction struct {
	Type          string                     `json:"type"`
	RedirectToURL PaymentIntentRedirectToURL `json:"redirect_to_url"`
}

type PaymentIntentRedirectToURL struct {
	URL       string `json:"url"`
	ReturnURL string `json:"return_url"`
}

type PaymentIntentError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Type    string `json:"type"`
}

type WebhookEvent struct {
	ID              string           `json:"id"`
	Object          string           `json:"object"`
	APIVersion      string           `json:"api_version"`
	Created         int64            `json:"created"`
	Data            WebhookEventData `json:"data"`
	Livemode        bool             `json:"livemode"`
	PendingWebhooks int              `json:"pending_webhooks"`
	Request         WebhookRequest   `json:"request"`
	Type            string           `json:"type"`
	Delivery        DeliveryAudit    `json:"-"`
}

type WebhookEventData struct {
	Object json.RawMessage `json:"object"`
}

type WebhookRequest struct {
	ID             string `json:"id"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type DeliveryAudit struct {
	Attempts       int    `json:"attempts"`
	LastStatusCode int    `json:"last_status_code,omitempty"`
	LastError      string `json:"last_error,omitempty"`
	LastAttemptAt  int64  `json:"last_attempt_at,omitempty"`
}

type idempotencyRecord struct {
	RequestHash string
	SessionID   string
}

type persistedState struct {
	Sessions           map[string]*CheckoutSession
	Intents            map[string]*PaymentIntent
	Idempotency        map[string]idempotencyRecord
	Events             map[string]*WebhookEvent
	EventOrder         []string
	BarionPayments     map[string]*BarionPayment
	BarionRequestIndex map[string]barionRequestRecord
	Registrations      map[string]*PaymentMethodRegistration
	Configuration      SimulatorConfiguration
	GeneratedCardSequences map[string]int
}

// SimulatorConfiguration selects the two externally visible QA branches. It
// deliberately contains no gateway infrastructure controls: MyScoutee still
// exercises its real Stripe/Barion adapters against this development service.
type SimulatorConfiguration struct {
	Provider         string `json:"provider"`
	Requires3DS      bool   `json:"requires3ds"`
	StripeCredential string `json:"-"`
	BarionCredential string `json:"-"`
}

// PaymentMethodRegistration is the provider-side result of a saved-card
// setup. Raw card numbers and security codes are deliberately absent: only a
// simulator token and the display-safe fields may survive the request.
type PaymentMethodRegistration struct {
	ID             string `json:"id"`
	Provider       string `json:"provider"`
	Status         string `json:"status"`
	URL            string `json:"url,omitempty"`
	ProviderToken  string `json:"providerToken,omitempty"`
	Brand          string `json:"brand,omitempty"`
	Last4          string `json:"last4,omitempty"`
	ExpiryMonth    int    `json:"expiryMonth,omitempty"`
	ExpiryYear     int    `json:"expiryYear,omitempty"`
	CardholderName string `json:"cardholderName,omitempty"`
	ExpiresAt      string `json:"expiresAt"`
	Requires3DS    bool   `json:"-"`
	Awaiting3DS    bool   `json:"awaiting3ds,omitempty"`
	ThreeDSExpires int64  `json:"threeDsExpiresAt,omitempty"`
	UserReference  string `json:"-"`
	ControlToken   string `json:"-"`
	CreatedAt      int64  `json:"-"`
	CallbackURL    string `json:"-"`
}

type auditResponse struct {
	Sessions       []CheckoutSessionAudit `json:"sessions"`
	PaymentIntents []PaymentIntentAudit   `json:"payment_intents"`
	Events         []WebhookEventAudit    `json:"events"`
	BarionPayments []BarionPaymentAudit   `json:"barion_payments"`
}

type CheckoutSessionAudit struct {
	ID                string             `json:"id"`
	Status            string             `json:"status"`
	PaymentStatus     string             `json:"payment_status"`
	AmountTotal       int64              `json:"amount_total"`
	Currency          string             `json:"currency"`
	ClientReferenceID string             `json:"client_reference_id,omitempty"`
	Metadata          map[string]string  `json:"metadata,omitempty"`
	Created           int64              `json:"created"`
	PaymentIntent     string             `json:"payment_intent"`
	LineItems         []CheckoutLineItem `json:"line_items,omitempty"`
}

type PaymentIntentAudit struct {
	ID                 string            `json:"id"`
	Status             string            `json:"status"`
	Amount             int64             `json:"amount"`
	AmountCapturable   int64             `json:"amount_capturable"`
	AmountReceived     int64             `json:"amount_received"`
	Currency           string            `json:"currency"`
	CaptureBefore      int64             `json:"capture_before,omitempty"`
	CancellationReason string            `json:"cancellation_reason,omitempty"`
	ClientReferenceID  string            `json:"client_reference_id,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	Created            int64             `json:"created"`
}

type WebhookEventAudit struct {
	ID       string        `json:"id"`
	Type     string        `json:"type"`
	ObjectID string        `json:"object_id"`
	Created  int64         `json:"created"`
	Delivery DeliveryAudit `json:"delivery"`
}
