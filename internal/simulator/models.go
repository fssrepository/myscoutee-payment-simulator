package simulator

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

type Server struct {
	config             Config
	now                func() time.Time
	client             *http.Client
	db                 *sql.DB
	mux                *http.ServeMux
	mu                 sync.RWMutex
	sessions           map[string]*CheckoutSession
	intents            map[string]*PaymentIntent
	idempotency        map[string]idempotencyRecord
	events             map[string]*WebhookEvent
	eventOrder         []string
	barionPayments     map[string]*BarionPayment
	barionRequestIndex map[string]barionRequestRecord
}

type CheckoutSession struct {
	ID                string            `json:"id"`
	Object            string            `json:"object"`
	URL               string            `json:"url"`
	Status            string            `json:"status"`
	PaymentStatus     string            `json:"payment_status"`
	Mode              string            `json:"mode"`
	AmountTotal       int64             `json:"amount_total"`
	Currency          string            `json:"currency"`
	SuccessURL        string            `json:"success_url"`
	CancelURL         string            `json:"cancel_url"`
	ClientReferenceID string            `json:"client_reference_id,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
	Created           int64             `json:"created"`
	Livemode          bool              `json:"livemode"`
	PaymentIntent     string            `json:"payment_intent"`
	ControlToken      string            `json:"-"`
	IdempotencyKey    string            `json:"-"`
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
}

type auditResponse struct {
	Sessions       []CheckoutSessionAudit `json:"sessions"`
	PaymentIntents []PaymentIntentAudit   `json:"payment_intents"`
	Events         []WebhookEventAudit    `json:"events"`
	BarionPayments []BarionPaymentAudit   `json:"barion_payments"`
}

type CheckoutSessionAudit struct {
	ID                string            `json:"id"`
	Status            string            `json:"status"`
	PaymentStatus     string            `json:"payment_status"`
	AmountTotal       int64             `json:"amount_total"`
	Currency          string            `json:"currency"`
	ClientReferenceID string            `json:"client_reference_id,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
	Created           int64             `json:"created"`
	PaymentIntent     string            `json:"payment_intent"`
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
